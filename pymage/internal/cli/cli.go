// Package cli implements the pymage command line.
package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"

	"github.com/imjasonh/terraform-playground/pymage/internal/build"
	"github.com/imjasonh/terraform-playground/pymage/internal/cache"
	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/sbom"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheelhouse"
)

// Root returns the root cobra command.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "pymage",
		Short:         "Docker-less, layer-aware Python container image builder",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(buildCmd())
	return root
}

type buildFlags struct {
	base       string
	lockFile   string
	findLinks  []string
	source     string
	tag        string
	platforms  []string
	pythonTag  string
	prefix     string
	workingDir string
	entrypoint []string
	cmd        []string
	env        []string
	user       string
	labels     []string
	ignore     []string
	strategy   string

	push        bool
	ociLayout   string
	printDigest bool
	sbomOut     string
	requireHash bool
	insecure    bool
	cacheDir    string
}

func buildCmd() *cobra.Command {
	f := &buildFlags{}
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a Python application image and push it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBuild(cmd.Context(), f)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&f.base, "base", "", "base image reference (required)")
	fs.StringVar(&f.lockFile, "lock", "", "hashed requirements file (required)")
	fs.StringArrayVar(&f.findLinks, "find-links", nil, "directory of wheels to resolve against (repeatable, required)")
	fs.StringVar(&f.source, "source", "", "application source directory (optional)")
	fs.StringVarP(&f.tag, "tag", "t", "", "image tag to push, e.g. registry/repo:tag")
	fs.StringSliceVar(&f.platforms, "platform", nil, "target platform(s); repeatable or comma-separated, e.g. linux/amd64,linux/arm64 (multiple builds a multi-arch index)")
	fs.StringVar(&f.pythonTag, "python", "python3.12", "python lib directory name for site-packages")
	fs.StringVar(&f.prefix, "prefix", "/app/.venv", "install prefix (venv-like root)")
	fs.StringVar(&f.workingDir, "workdir", "/app", "image working directory and source destination")
	fs.StringArrayVar(&f.entrypoint, "entrypoint", nil, "entrypoint argv (repeatable)")
	fs.StringArrayVar(&f.cmd, "cmd", nil, "cmd argv (repeatable)")
	fs.StringArrayVar(&f.env, "env", nil, "extra KEY=VALUE env (repeatable)")
	fs.StringVar(&f.user, "user", "", "image user, e.g. 65532")
	fs.StringArrayVar(&f.labels, "label", nil, "image label KEY=VALUE (repeatable)")
	fs.StringArrayVar(&f.ignore, "ignore", nil, "extra source ignore glob (repeatable)")
	fs.StringVar(&f.strategy, "layer-strategy", string(build.PerWheel), "per-wheel | single-deps-layer")
	fs.BoolVar(&f.push, "push", true, "push the image to --tag")
	fs.StringVar(&f.ociLayout, "oci-layout", "", "also write the image to this OCI layout directory")
	fs.BoolVar(&f.printDigest, "print-digest", false, "print only the resulting image digest")
	fs.StringVar(&f.sbomOut, "sbom", "", "write a CycloneDX SBOM to this path")
	fs.BoolVar(&f.requireHash, "require-hashes", true, "require every requirement to carry --hash entries")
	fs.BoolVar(&f.insecure, "insecure", false, "use plain HTTP for the registry")
	fs.StringVar(&f.cacheDir, "cache-dir", "", "directory for the content-addressed layer cache (speeds up rebuilds)")
	return cmd
}

func runBuild(ctx context.Context, f *buildFlags) error {
	if f.base == "" {
		return fmt.Errorf("--base is required")
	}
	if f.lockFile == "" {
		return fmt.Errorf("--lock is required")
	}
	if len(f.findLinks) == 0 {
		return fmt.Errorf("--find-links is required (a directory of wheels)")
	}

	if _, err := keyValues(f.env); err != nil {
		return fmt.Errorf("--env: %w", err)
	}
	labels, err := keyValues(f.labels)
	if err != nil {
		return fmt.Errorf("--label: %w", err)
	}

	reqs, err := lock.ParseFile(f.lockFile)
	if err != nil {
		return err
	}
	if err := lock.Validate(reqs, f.requireHash); err != nil {
		return err
	}

	platforms, err := parsePlatforms(f.platforms)
	if err != nil {
		return err
	}

	var nameOpts []name.Option
	if f.insecure {
		nameOpts = append(nameOpts, name.Insecure)
	}

	var layerCache *cache.Cache
	if f.cacheDir != "" {
		layerCache, err = cache.New(f.cacheDir)
		if err != nil {
			return err
		}
	}

	// Build one image per target platform. With more than one platform we
	// assemble them into a multi-arch OCI image index; with zero or one we push
	// a plain image manifest.
	var (
		images  []v1.Image
		adds    []mutate.IndexAddendum
		allDeps []wheelhouse.ResolvedWheel
	)
	targets := platforms
	if len(targets) == 0 {
		targets = []v1.Platform{{}} // single build using registry defaults
	}
	for i := range targets {
		p := targets[i]
		var pp *v1.Platform
		if p.OS != "" || p.Architecture != "" {
			pp = &p
		}
		img, deps, err := buildOne(ctx, f, reqs, pp, labels, layerCache, nameOpts)
		if err != nil {
			return err
		}
		images = append(images, img)
		allDeps = append(allDeps, deps...)
		desc := v1.Descriptor{}
		if pp != nil {
			desc.Platform = pp
		}
		adds = append(adds, mutate.IndexAddendum{Add: img, Descriptor: desc})
	}

	if f.sbomOut != "" {
		data, err := sbom.Generate(dedupeWheels(allDeps))
		if err != nil {
			return err
		}
		if err := os.WriteFile(f.sbomOut, data, 0o644); err != nil {
			return err
		}
	}

	if len(platforms) > 1 {
		idx := mutate.AppendManifests(empty.Index, adds...)
		return output(ctx, f, nameOpts, nil, idx)
	}
	return output(ctx, f, nameOpts, images[0], nil)
}

// buildOne resolves the base and wheels for a single platform and builds the
// image, returning the resolved wheels for SBOM aggregation.
func buildOne(ctx context.Context, f *buildFlags, reqs []lock.Requirement, platform *v1.Platform, labels map[string]string, layerCache *cache.Cache, nameOpts []name.Option) (v1.Image, []wheelhouse.ResolvedWheel, error) {
	target, err := resolveTarget(platform, f.pythonTag)
	if err != nil {
		return nil, nil, err
	}
	wheels, err := wheelhouse.Resolve(reqs, f.findLinks, target)
	if err != nil {
		return nil, nil, err
	}
	base, err := resolveBase(ctx, f.base, platform, nameOpts...)
	if err != nil {
		return nil, nil, err
	}
	img, err := build.Build(build.Options{
		Base:       base,
		Wheels:     wheels,
		Layout:     wheel.Layout{Prefix: f.prefix, PythonTag: f.pythonTag},
		Strategy:   build.LayerStrategy(f.strategy),
		SourceDir:  f.source,
		Ignore:     f.ignore,
		WorkingDir: f.workingDir,
		Entrypoint: f.entrypoint,
		Cmd:        f.cmd,
		Env:        f.env,
		User:       f.user,
		Labels:     labels,
		Cache:      layerCache,
	})
	if err != nil {
		return nil, nil, err
	}
	return img, wheels, nil
}

// output writes/pushes either a single image or a multi-arch index (exactly one
// of img/idx is non-nil).
func output(ctx context.Context, f *buildFlags, nameOpts []name.Option, img v1.Image, idx v1.ImageIndex) error {
	var (
		digest v1.Hash
		err    error
	)
	if idx != nil {
		digest, err = idx.Digest()
	} else {
		digest, err = img.Digest()
	}
	if err != nil {
		return err
	}

	if f.ociLayout != "" {
		p, err := layout.Write(f.ociLayout, empty.Index)
		if err != nil {
			return err
		}
		if idx != nil {
			err = p.AppendIndex(idx)
		} else {
			err = p.AppendImage(img)
		}
		if err != nil {
			return err
		}
	}

	if f.printDigest {
		fmt.Println(digest.String())
		return nil
	}

	if f.push {
		if f.tag == "" {
			return fmt.Errorf("--tag is required to push (or use --push=false)")
		}
		ref, err := name.ParseReference(f.tag, nameOpts...)
		if err != nil {
			return err
		}
		opts := []remote.Option{
			remote.WithContext(ctx),
			remote.WithAuthFromKeychain(authn.DefaultKeychain),
		}
		if idx != nil {
			err = remote.WriteIndex(ref, idx, opts...)
		} else {
			err = remote.Write(ref, img, opts...)
		}
		if err != nil {
			return fmt.Errorf("push: %w", err)
		}
		fmt.Printf("pushed %s@%s\n", ref.Context().Name(), digest)
		return nil
	}

	fmt.Println(digest.String())
	return nil
}

// parsePlatforms parses the (comma-split, repeatable) --platform values.
func parsePlatforms(specs []string) ([]v1.Platform, error) {
	var out []v1.Platform
	for _, s := range specs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		p, err := v1.ParsePlatform(s)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

// dedupeWheels removes duplicate wheels (same name+version+sha) so a multi-arch
// SBOM doesn't list shared pure-python wheels multiple times.
func dedupeWheels(in []wheelhouse.ResolvedWheel) []wheelhouse.ResolvedWheel {
	seen := map[string]bool{}
	var out []wheelhouse.ResolvedWheel
	for _, w := range in {
		k := w.Name + "\x00" + w.Version + "\x00" + w.SHA256
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, w)
	}
	return out
}

// resolveTarget derives the wheel-selection target (platform + interpreter)
// from the --platform and --python flags, defaulting to linux/amd64.
func resolveTarget(platform *v1.Platform, pythonTag string) (wheel.Target, error) {
	t := wheel.Target{OS: "linux", Arch: "amd64"}
	if platform != nil {
		if platform.OS != "" {
			t.OS = platform.OS
		}
		if platform.Architecture != "" {
			t.Arch = platform.Architecture
		}
	}
	maj, min, err := parsePythonTag(pythonTag)
	if err != nil {
		return wheel.Target{}, err
	}
	t.PyMajor, t.PyMinor = maj, min
	return t, nil
}

// parsePythonTag parses "python3.12" into (3, 12).
func parsePythonTag(tag string) (int, int, error) {
	s := strings.TrimPrefix(tag, "python")
	majStr, minStr, ok := strings.Cut(s, ".")
	if !ok {
		return 0, 0, fmt.Errorf("--python %q must look like pythonX.Y", tag)
	}
	maj, err := strconv.Atoi(majStr)
	if err != nil {
		return 0, 0, fmt.Errorf("--python %q: bad major version", tag)
	}
	min, err := strconv.Atoi(minStr)
	if err != nil {
		return 0, 0, fmt.Errorf("--python %q: bad minor version", tag)
	}
	return maj, min, nil
}

func resolveBase(ctx context.Context, ref string, platform *v1.Platform, nameOpts ...name.Option) (v1.Image, error) {
	if ref == "scratch" {
		return empty.Image, nil
	}
	return build.Base(ctx, ref, platform, authn.DefaultKeychain, nameOpts...)
}

func keyValues(in []string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range in {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("expected KEY=VALUE with a non-empty key, got %q", kv)
		}
		out[k] = v
	}
	return out, nil
}
