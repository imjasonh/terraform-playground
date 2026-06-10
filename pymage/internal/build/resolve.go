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
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Base resolves a base image reference to a v1.Image for the given platform.
// Only the base manifest and config are fetched; its layer blobs are referenced
// by digest and never downloaded.
func Base(ctx context.Context, ref string, platform *v1.Platform, kc authn.Keychain, nameOpts ...name.Option) (v1.Image, error) {
	r, err := name.ParseReference(ref, nameOpts...)
	if err != nil {
		return nil, fmt.Errorf("build: parse base ref %q: %w", ref, err)
	}
	if kc == nil {
		kc = authn.DefaultKeychain
	}
	opts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(kc),
	}
	if platform != nil {
		opts = append(opts, remote.WithPlatform(*platform))
	}
	img, err := remote.Image(r, opts...)
	if err != nil {
		return nil, fmt.Errorf("build: fetch base %q: %w", ref, err)
	}
	return img, nil
}

// apkoMaxBytes caps how much of the apko.json file we read.
const apkoMaxBytes = 4 << 20

// pythonPkgRE matches an apko "python-X.Y" package name (with or without the
// "-base" suffix), as found in a Chainguard/Wolfi image's apko.json.
var pythonPkgRE = regexp.MustCompile(`^python-(\d+)\.(\d+)(?:-base)?$`)

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
