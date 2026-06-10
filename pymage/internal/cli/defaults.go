package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

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
	// --env flags last so they win on duplicate keys.
	f.env = append(append(append([]string{}, proj.ExtraEnv...), cfg.Env...), f.env...)

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

func wheelCacheDir(f *buildFlags) (string, error) {
	if f.cacheDir != "" {
		return filepath.Join(f.cacheDir, "wheels"), nil
	}
	return project.DefaultWheelCacheDir()
}

func validateBuildFlags(f *buildFlags) error {
	if f.lockFile == "" {
		return fmt.Errorf("no lock file found (expected uv.lock in the source directory, or pass --lock)")
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
