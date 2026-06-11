package build

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// apkoMaxBytes caps how much of the apko.json file we read.
const apkoMaxBytes = 4 << 20

// pythonPkgRE matches an apko "python-X.Y" package name (with or without the
// "-base" suffix), as found in a Chainguard/Wolfi image's apko.json.
var pythonPkgRE = regexp.MustCompile(`^python-(\d+)\.(\d+)(?:-base)?$`)

// BaseSet is a base image reference resolved once and reused across platforms.
// It fetches the base index/manifest a single time, then serves per-platform
// child images on demand (memoized), avoiding the redundant index re-fetch that
// a separate remote.Image call per platform would incur.
type BaseSet struct {
	ref       string
	img       v1.Image      // set for a single-platform base (or scratch)
	idx       v1.ImageIndex // set for a multi-platform base
	platforms []v1.Platform
	children  map[string]v1.Image
}

// ResolveBaseSet fetches ref once and returns a BaseSet. "scratch" resolves to
// an empty base with a single (registry-default) platform and no network I/O.
func ResolveBaseSet(ctx context.Context, ref string, kc authn.Keychain, nameOpts ...name.Option) (*BaseSet, error) {
	if ref == "scratch" {
		return &BaseSet{
			ref:       ref,
			img:       empty.Image,
			platforms: []v1.Platform{{}},
			children:  map[string]v1.Image{},
		}, nil
	}

	r, err := name.ParseReference(ref, nameOpts...)
	if err != nil {
		return nil, fmt.Errorf("build: parse base ref %q: %w", ref, err)
	}
	if kc == nil {
		kc = authn.DefaultKeychain
	}
	desc, err := remote.Get(r, remote.WithContext(ctx), remote.WithAuthFromKeychain(kc))
	if err != nil {
		return nil, fmt.Errorf("build: inspect base %q: %w", ref, err)
	}

	bs := &BaseSet{ref: ref, children: map[string]v1.Image{}}
	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return nil, err
		}
		im, err := idx.IndexManifest()
		if err != nil {
			return nil, err
		}
		bs.idx = idx
		seen := map[string]bool{}
		for _, m := range im.Manifests {
			p := m.Platform
			if p == nil || p.OS == "" || p.Architecture == "" {
				continue
			}
			// Skip attestation/SBOM placeholders (buildx uses unknown/unknown).
			if p.OS == "unknown" || p.Architecture == "unknown" {
				continue
			}
			key := platformKey(p)
			if seen[key] {
				continue
			}
			seen[key] = true
			bs.platforms = append(bs.platforms, v1.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant})
		}
		if len(bs.platforms) == 0 {
			return nil, fmt.Errorf("build: base index %q advertises no usable platforms", ref)
		}
		return bs, nil
	}

	img, err := desc.Image()
	if err != nil {
		return nil, err
	}
	cf, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	if cf.OS == "" || cf.Architecture == "" {
		return nil, fmt.Errorf("build: base image %q has no platform in its config", ref)
	}
	bs.img = img
	bs.platforms = []v1.Platform{{OS: cf.OS, Architecture: cf.Architecture, Variant: cf.Variant}}
	return bs, nil
}

// Platforms returns the platforms the base supports.
func (b *BaseSet) Platforms() []v1.Platform { return b.platforms }

// Image returns the base image for the given platform (nil platform selects the
// single available image). Child images are fetched at most once.
func (b *BaseSet) Image(platform *v1.Platform) (v1.Image, error) {
	if b.img != nil {
		return b.img, nil
	}
	want := &v1.Platform{}
	if platform != nil {
		want = platform
	}
	key := platformKey(want)
	if img, ok := b.children[key]; ok {
		return img, nil
	}

	im, err := b.idx.IndexManifest()
	if err != nil {
		return nil, err
	}
	for _, m := range im.Manifests {
		if m.Platform == nil {
			continue
		}
		if m.Platform.OS == want.OS && m.Platform.Architecture == want.Architecture &&
			(want.Variant == "" || m.Platform.Variant == want.Variant) {
			img, err := b.idx.Image(m.Digest)
			if err != nil {
				return nil, err
			}
			b.children[key] = img
			return img, nil
		}
	}
	return nil, fmt.Errorf("build: base %q has no image for platform %s", b.ref, key)
}

func platformKey(p *v1.Platform) string {
	if p == nil {
		return ""
	}
	return p.OS + "/" + p.Architecture + "/" + p.Variant
}

// InterpreterVersion reports the Python X.Y the base image provides.
//
// It first checks a PYTHON_VERSION env var (set by the official python images),
// which is free since the config is already fetched. If that is absent it falls
// back to reading /etc/apko.json from the top-most layer and parsing the
// "python-X.Y" package (Chainguard/Wolfi images, which don't set the env var);
// this fetches only that one layer. When neither is present, ok is false and
// the interpreter cannot be validated — a reason to pin a known base.
func InterpreterVersion(img v1.Image) (major, minor int, ok bool) {
	if maj, min, ok := interpreterFromEnv(img); ok {
		return maj, min, ok
	}
	return interpreterFromAPKO(img)
}

func interpreterFromEnv(img v1.Image) (major, minor int, ok bool) {
	cf, err := img.ConfigFile()
	if err != nil || cf == nil {
		return 0, 0, false
	}
	for _, kv := range cf.Config.Env {
		k, v, found := strings.Cut(kv, "=")
		if !found || k != "PYTHON_VERSION" {
			continue
		}
		return parseMajorMinor(v)
	}
	return 0, 0, false
}

// interpreterFromAPKO reads /etc/apko.json from the top-most layer and extracts
// the Python version from its package list.
func interpreterFromAPKO(img v1.Image) (major, minor int, ok bool) {
	layers, err := img.Layers()
	if err != nil || len(layers) == 0 {
		return 0, 0, false
	}
	data, found := readFileFromLayer(layers[len(layers)-1], "etc/apko.json")
	if !found {
		return 0, 0, false
	}
	var doc struct {
		Contents struct {
			Packages []string `json:"packages"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, 0, false
	}
	for _, pkg := range doc.Contents.Packages {
		// Entries look like "python-3.14=3.14.5-r2"; take the package name.
		name := pkg
		if i := strings.IndexAny(pkg, "=<>~ "); i >= 0 {
			name = pkg[:i]
		}
		if m := pythonPkgRE.FindStringSubmatch(name); m != nil {
			maj, err1 := strconv.Atoi(m[1])
			min, err2 := strconv.Atoi(m[2])
			if err1 == nil && err2 == nil {
				return maj, min, true
			}
		}
	}
	return 0, 0, false
}

// readFileFromLayer returns the contents of name (a slash path without a
// leading slash) from a layer's uncompressed tar, if present.
func readFileFromLayer(layer v1.Layer, name string) ([]byte, bool) {
	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, false
	}
	defer func() { _ = rc.Close() }()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, false
		}
		entry := strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/")
		if entry != name || h.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, apkoMaxBytes))
		if err != nil {
			return nil, false
		}
		return data, true
	}
}

func parseMajorMinor(v string) (major, minor int, ok bool) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}
