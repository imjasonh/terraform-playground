package cli

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imjasonh/terraform-playground/pymage/internal/build"
	"github.com/imjasonh/terraform-playground/pymage/internal/cache"
	"github.com/imjasonh/terraform-playground/pymage/internal/project"
)

// applyDefaults fills omitted flags from the uv project in the source directory
// (the positional arg, default "."). Precedence is: explicit CLI flag >
// [tool.pymage] in pyproject.toml >
// built-in default. A flag counts as "explicit" only when the user actually
// passed it, which we detect via cobra's Changed() so flags with non-empty
// defaults (e.g. --workdir) can still be overridden by config.
func applyDefaults(cmd *cobra.Command, f *buildFlags) error {
	if f.source == "" {
		f.source = "."
	}
	proj, discoverErr := project.Discover(f.source)
	cfg := proj.Config
	changed := func(name string) bool { return cmd.Flags().Changed(name) }

	if f.lockFile == "" {
		if proj.LockFile == "" {
			if discoverErr != nil {
				return discoverErr
			}
			return fmt.Errorf("no lock file found (expected uv.lock in the source directory, or pass --lock)")
		}
		f.lockFile = proj.LockFile
	}

	if !changed("repo") && cfg.Repo != "" {
		f.repo = cfg.Repo
	}
	if !changed("tag") && len(cfg.Tags) > 0 {
		f.tags = cfg.Tags
	}
	if !changed("base") && cfg.Base != "" {
		f.base = cfg.Base
	}
	if f.base == "" {
		f.base = project.DefaultBase()
	}
	if !changed("platform") && len(cfg.Platforms) > 0 {
		f.platforms = cfg.Platforms
	}
	if !changed("layer-strategy") && cfg.LayerStrategy != "" {
		f.strategy = cfg.LayerStrategy
	}
	if !changed("max-layers") && cfg.MaxLayers != 0 {
		f.maxLayers = cfg.MaxLayers
	}
	if !changed("max-wheel-layers") && cfg.MaxWheelLayers != 0 {
		f.maxWheelLayers = cfg.MaxWheelLayers
	}
	if !changed("push-concurrency") && cfg.PushJobs != 0 {
		f.pushJobs = cfg.PushJobs
	}
	if !changed("no-cache") && cfg.NoCache {
		f.noCache = true
	}
	if !changed("extra") && len(cfg.Extras) > 0 {
		f.extras = cfg.Extras
	}
	if !changed("package") && cfg.Package != "" {
		f.pkg = cfg.Package
	}
	if !changed("repack-sdists") && cfg.RepackSdists {
		f.repackSdists = true
	}
	if !changed("python") && cfg.Python != "" {
		f.pythonTag = cfg.Python
	}
	if !changed("prefix") && cfg.Prefix != "" {
		f.prefix = cfg.Prefix
	}
	if !changed("workdir") && cfg.Workdir != "" {
		f.workingDir = cfg.Workdir
	}
	if !changed("user") && cfg.User != "" {
		f.user = cfg.User
	}
	if !changed("find-links") && len(cfg.FindLinks) > 0 {
		f.findLinks = cfg.FindLinks
	}
	if !changed("cmd") && len(cfg.Cmd) > 0 {
		f.cmd = cfg.Cmd
	}

	// Entrypoint: explicit flag > config > auto-detected console script.
	if len(f.entrypoint) == 0 {
		switch {
		case len(cfg.Entrypoint) > 0:
			f.entrypoint = cfg.Entrypoint
		case len(proj.Entrypoint) > 0:
			f.entrypoint = proj.Entrypoint
		}
	}

	// Env is additive: auto-detected (PYTHONPATH) first, then config, then any
	// --env flags last so they win on duplicate keys. The src-layout PYTHONPATH
	// is derived from the resolved workdir, since the source is copied there.
	var autoEnv []string
	if proj.SrcLayout {
		autoEnv = append(autoEnv, "PYTHONPATH="+path.Join(f.workingDir, "src"))
	}
	f.env = append(append(autoEnv, cfg.Env...), f.env...)

	// Labels: config labels first, --label flags appended so they override.
	if len(cfg.Labels) > 0 {
		keys := make([]string, 0, len(cfg.Labels))
		for k := range cfg.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		merged := make([]string, 0, len(cfg.Labels)+len(f.labels))
		for _, k := range keys {
			merged = append(merged, k+"="+cfg.Labels[k])
		}
		f.labels = append(merged, f.labels...)
	}

	return nil
}

// setupCaches resolves the build caches. Caching is on by default; --no-cache
// disables persistent caching (downloads still work, via an ephemeral temp dir
// that the returned cleanup removes). The layer cache also serves the small
// metadata cache (interpreter detection), so they share one directory.
func setupCaches(f *buildFlags) (layer, meta *cache.Cache, wheelDir string, cleanup func(), err error) {
	noop := func() {}
	if f.noCache {
		tmp, err := os.MkdirTemp("", "pymage-wheels-")
		if err != nil {
			return nil, nil, "", noop, err
		}
		return nil, nil, tmp, func() { _ = os.RemoveAll(tmp) }, nil
	}

	root := f.cacheDir
	if root == "" {
		root, err = project.DefaultCacheDir()
		if err != nil {
			return nil, nil, "", noop, err
		}
	}
	c, err := cache.New(root)
	if err != nil {
		return nil, nil, "", noop, err
	}
	return c, c, filepath.Join(root, "wheels"), noop, nil
}

func validateBuildFlags(f *buildFlags) error {
	if f.lockFile == "" {
		return fmt.Errorf("no lock file found (expected uv.lock in the source directory, or pass --lock)")
	}
	// "" is the zero value meaning the default (Auto); a 0 budget likewise means
	// "use the default", so only reject genuinely invalid values.
	switch f.strategy {
	case "", string(build.Auto), string(build.PerWheel), string(build.SingleDepsLayer):
	default:
		return fmt.Errorf("invalid --layer-strategy %q (want auto, per-wheel, or single-deps-layer)", f.strategy)
	}
	if f.maxLayers < 0 {
		return fmt.Errorf("--max-layers must be >= 0 (0 means use the default), got %d", f.maxLayers)
	}
	if f.maxWheelLayers < 0 {
		return fmt.Errorf("--max-wheel-layers must be >= 0, got %d", f.maxWheelLayers)
	}
	if f.pushJobs < 0 {
		return fmt.Errorf("--push-concurrency must be >= 0, got %d", f.pushJobs)
	}
	if len(f.entrypoint) == 0 {
		return fmt.Errorf("no entrypoint: set [project.scripts] in pyproject.toml, add entrypoint to [tool.pymage], or pass --entrypoint")
	}
	if f.push {
		if f.repo == "" {
			return fmt.Errorf("no repo configured: set repo in [tool.pymage] (pyproject.toml) or pass --repo (or use --push=false)")
		}
		for _, t := range f.tags {
			if strings.ContainsAny(t, "/:@") {
				return fmt.Errorf("--tag/-t is a tag only, not a full reference: set the destination with --repo or [tool.pymage] repo (got %q)", t)
			}
		}
	}
	return nil
}
