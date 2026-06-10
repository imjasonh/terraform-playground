// Package cli implements the pymage command line.
package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
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
	repo       string
	tags       []string
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

	maxLayers      int
	maxWheelLayers int

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
		Use:   "build [source]",
		Short: "Build a Python application image and push it",
		Long: "Build a Python application image and push it.\n\n" +
			"[source] is the uv project directory to build (default: current directory).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				f.source = args[0]
			}
			return runBuild(cmd, f)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&f.base, "base", "", "base image reference (default: cgr.dev/chainguard/python:latest)")
	fs.StringVar(&f.lockFile, "lock", "", "lock file (default: uv.lock in the source directory)")
	fs.StringArrayVar(&f.findLinks, "find-links", nil, "local wheel directory (optional; wheels are downloaded from the lock when omitted)")
	fs.StringVar(&f.repo, "repo", "", "destination repository, pushed by digest, e.g. gcr.io/foo/bar (default: [tool.pymage] repo)")
	fs.StringArrayVarP(&f.tags, "tag", "t", nil, "tag(s) to apply at --repo; tag component only, not a full reference (repeatable; default: [tool.pymage] tags or 'latest')")
	fs.StringSliceVar(&f.platforms, "platform", nil, "target platform(s); repeatable or comma-separated, e.g. linux/amd64,linux/arm64 (multiple builds a multi-arch index)")
	fs.StringVar(&f.pythonTag, "python", "", "interpreter version, e.g. python3.12 (default: auto-detected from the base image; if set, must match the base)")
	fs.StringVar(&f.prefix, "prefix", "/app/.venv", "install prefix (venv-like root)")
	fs.StringVar(&f.workingDir, "workdir", "/app", "image working directory and source destination")
	fs.StringArrayVar(&f.entrypoint, "entrypoint", nil, "entrypoint argv (repeatable)")
	fs.StringArrayVar(&f.cmd, "cmd", nil, "cmd argv (repeatable)")
	fs.StringArrayVar(&f.env, "env", nil, "extra KEY=VALUE env (repeatable)")
	fs.StringVar(&f.user, "user", "", "image user, e.g. 65532")
	fs.StringArrayVar(&f.labels, "label", nil, "image label KEY=VALUE (repeatable)")
	fs.StringArrayVar(&f.ignore, "ignore", nil, "extra source ignore glob (repeatable)")
	fs.StringVar(&f.strategy, "layer-strategy", string(build.Auto), "auto | per-wheel | single-deps-layer")
	fs.IntVar(&f.maxLayers, "max-layers", build.DefaultMaxLayers, "max total image layers (base + deps + app) for the auto strategy")
	fs.IntVar(&f.maxWheelLayers, "max-wheel-layers", 0, "cap the number of dependency layers directly (overrides --max-layers when > 0)")
	fs.BoolVar(&f.push, "push", true, "push the image to --repo (by digest)")
	fs.StringVar(&f.ociLayout, "oci-layout", "", "also write the image to this OCI layout directory")
	fs.BoolVar(&f.printDigest, "print-digest", false, "print only the resulting image digest")
	fs.StringVar(&f.sbomOut, "sbom", "", "write a CycloneDX SBOM to this path")
	fs.BoolVar(&f.requireHash, "require-hashes", true, "require every requirement to carry --hash entries")
	fs.BoolVar(&f.insecure, "insecure", false, "use plain HTTP for the registry")
	fs.StringVar(&f.cacheDir, "cache-dir", "", "directory for the content-addressed layer cache (speeds up rebuilds)")
	return cmd
}

func runBuild(cmd *cobra.Command, f *buildFlags) error {
	ctx := cmd.Context()
	// Keep stdout clean (only the final image ref): route go-containerregistry's
	// per-blob progress ("existing blob", "mounted blob", "pushed blob") and
	// warnings to stderr.
	logs.Progress.SetOutput(cmd.ErrOrStderr())
	logs.Warn.SetOutput(cmd.ErrOrStderr())

	if err := applyDefaults(cmd, f); err != nil {
		return err
	}
	if err := validateBuildFlags(f); err != nil {
		return err
	}

	if _, err := keyValues(f.env); err != nil {
		return fmt.Errorf("--env: %w", err)
	}
	labels, err := keyValues(f.labels)
	if err != nil {
		return fmt.Errorf("--label: %w", err)
	}

	reqs, err := lock.ParseAny(f.lockFile)
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

	// With no platform requested, default to whatever the base image supports
	// (a multi-arch base yields a multi-arch build).
	if len(platforms) == 0 {
		platforms, err = defaultPlatformsFromBase(ctx, f, nameOpts)
		if err != nil {
			return err
		}
	}

	var layerCache *cache.Cache
	wheelCache, err := wheelCacheDir(f)
	if err != nil {
		return err
	}
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
	for i := range targets {
		p := targets[i]
		var pp *v1.Platform
		if p.OS != "" || p.Architecture != "" {
			pp = &targets[i]
		}
		img, deps, err := buildOne(ctx, f, reqs, pp, labels, layerCache, wheelCache, nameOpts)
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
		return output(cmd, f, nameOpts, nil, idx)
	}
	return output(cmd, f, nameOpts, images[0], nil)
}

// buildOne resolves the base and wheels for a single platform and builds the
// image, returning the resolved wheels for SBOM aggregation.
func buildOne(ctx context.Context, f *buildFlags, reqs []lock.Requirement, platform *v1.Platform, labels map[string]string, layerCache *cache.Cache, wheelCache string, nameOpts []name.Option) (v1.Image, []wheelhouse.ResolvedWheel, error) {
	base, err := resolveBase(ctx, f.base, platform, nameOpts...)
	if err != nil {
		return nil, nil, err
	}

	// Determine the interpreter: honor --python (validated against the base) or
	// auto-detect it from the base image.
	major, minor, pyTag, err := resolveInterpreter(f, base)
	if err != nil {
		return nil, nil, err
	}

	target := platformTarget(platform)
	target.PyMajor, target.PyMinor = major, minor

	wheels, err := wheelhouse.ResolveContext(ctx, reqs, f.findLinks, target, wheelCache)
	if err != nil {
		return nil, nil, err
	}
	img, err := build.Build(build.Options{
		Base:           base,
		Wheels:         wheels,
		Layout:         wheel.Layout{Prefix: f.prefix, PythonTag: pyTag},
		Strategy:       build.LayerStrategy(f.strategy),
		MaxLayers:      f.maxLayers,
		MaxWheelLayers: f.maxWheelLayers,
		SourceDir:      f.source,
		Ignore:         f.ignore,
		WorkingDir:     f.workingDir,
		Entrypoint:     f.entrypoint,
		Cmd:            f.cmd,
		Env:            f.env,
		User:           f.user,
		Labels:         labels,
		Cache:          layerCache,
	})
	if err != nil {
		return nil, nil, err
	}
	return img, wheels, nil
}

// resolveInterpreter decides the target Python version and the site-packages
// directory tag. If --python is set it is authoritative but must match the
// base when the base advertises a version; otherwise the version is detected
// from the base image.
func resolveInterpreter(f *buildFlags, base v1.Image) (major, minor int, pyTag string, err error) {
	baseMaj, baseMin, baseOK := build.InterpreterVersion(base)

	if f.pythonTag != "" {
		maj, min, err := parsePythonTag(f.pythonTag)
		if err != nil {
			return 0, 0, "", err
		}
		if baseOK && (baseMaj != maj || baseMin != min) {
			return 0, 0, "", fmt.Errorf("base %q provides Python %d.%d but --python is python%d.%d; pass --python python%d.%d (or pin a matching base)",
				f.base, baseMaj, baseMin, maj, min, baseMaj, baseMin)
		}
		return maj, min, f.pythonTag, nil
	}

	if !baseOK {
		return 0, 0, "", fmt.Errorf("could not determine the base image's Python version (no PYTHON_VERSION env or python package in /etc/apko.json); pass --python pythonX.Y")
	}
	return baseMaj, baseMin, fmt.Sprintf("python%d.%d", baseMaj, baseMin), nil
}

// output writes/pushes either a single image or a multi-arch index (exactly one
// of img/idx is non-nil).
//
// stdout carries exactly one line — the resulting image reference — so callers
// can run `docker run "$(pymage build)"`. All progress/diagnostic output goes
// to stderr.
func output(cmd *cobra.Command, f *buildFlags, nameOpts []name.Option, img v1.Image, idx v1.ImageIndex) error {
	ctx := cmd.Context()
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	digest, err := artifactDigest(img, idx)
	if err != nil {
		return err
	}

	if f.ociLayout != "" {
		if err := writeLayout(f.ociLayout, img, idx); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "wrote OCI layout to %s\n", f.ociLayout)
	}

	if f.printDigest {
		_, _ = fmt.Fprintln(stdout, digest.String())
		return nil
	}

	if !f.push {
		_, _ = fmt.Fprintln(stdout, digest.String())
		return nil
	}

	if f.repo == "" {
		return fmt.Errorf("no repo configured: set repo in [tool.pymage] or pass --repo (or use --push=false)")
	}
	repo, err := name.NewRepository(f.repo, nameOpts...)
	if err != nil {
		return err
	}
	tags := f.tags
	if len(tags) == 0 {
		tags = []string{"latest"}
	}
	opts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	}
	// The artifact is pushed by content; each tag is just another pointer to
	// the same digest.
	for _, t := range tags {
		ref := repo.Tag(t)
		if err := writeArtifact(ref, img, idx, opts...); err != nil {
			return fmt.Errorf("push %s: %w", ref, err)
		}
		_, _ = fmt.Fprintf(stderr, "tagged %s -> %s@%s\n", ref, repo.Name(), digest)
	}

	// The sole stdout line: the immutable by-digest reference.
	_, _ = fmt.Fprintf(stdout, "%s@%s\n", repo.Name(), digest)
	return nil
}

func artifactDigest(img v1.Image, idx v1.ImageIndex) (v1.Hash, error) {
	if idx != nil {
		return idx.Digest()
	}
	return img.Digest()
}

func writeArtifact(ref name.Reference, img v1.Image, idx v1.ImageIndex, opts ...remote.Option) error {
	if idx != nil {
		return remote.WriteIndex(ref, idx, opts...)
	}
	return remote.Write(ref, img, opts...)
}

func writeLayout(dir string, img v1.Image, idx v1.ImageIndex) error {
	p, err := layout.Write(dir, empty.Index)
	if err != nil {
		return err
	}
	if idx != nil {
		return p.AppendIndex(idx)
	}
	return p.AppendImage(img)
}

// defaultPlatformsFromBase returns the platforms to build for when --platform
// is not given: those advertised by the base image. A scratch base has no
// registry metadata, so it falls back to a single registry-default build.
func defaultPlatformsFromBase(ctx context.Context, f *buildFlags, nameOpts []name.Option) ([]v1.Platform, error) {
	if f.base == "scratch" {
		return []v1.Platform{{}}, nil
	}
	return build.BasePlatforms(ctx, f.base, authn.DefaultKeychain, nameOpts...)
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

// platformTarget derives the OS/arch part of the wheel-selection target from
// the --platform value, defaulting to linux/amd64. The interpreter fields are
// filled in by the caller.
func platformTarget(platform *v1.Platform) wheel.Target {
	t := wheel.Target{OS: "linux", Arch: "amd64"}
	if platform != nil {
		if platform.OS != "" {
			t.OS = platform.OS
		}
		if platform.Architecture != "" {
			t.Arch = platform.Architecture
		}
	}
	return t
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
