// Package build assembles an OCI image from a base image, a set of resolved
// wheels (each its own deterministic layer by default), and an application
// source layer on top.
//
// The output is reproducible: identical inputs (same base digest, same wheels,
// same source) yield an identical image digest. This is what lets a rebuild
// transfer no dependency bytes — every unchanged layer keeps its digest and is
// already present in the registry.
package build

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"

	"github.com/imjasonh/terraform-playground/pymage/internal/ptar"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheelhouse"
)

// LayerStrategy controls how dependency layers are partitioned.
type LayerStrategy string

const (
	// PerWheel gives each wheel its own layer. Adding a dependency creates
	// exactly one new layer and leaves the rest byte-identical. (default)
	PerWheel LayerStrategy = "per-wheel"
	// SingleDepsLayer collapses all wheels into one layer. Simplest, but any
	// dependency change rebuilds the whole layer.
	SingleDepsLayer LayerStrategy = "single-deps-layer"
)

// Options configures an image build.
type Options struct {
	// Base is the resolved base image (use Base to resolve a reference).
	Base v1.Image
	// Wheels are the resolved dependency wheels, already in deterministic order.
	Wheels []wheelhouse.ResolvedWheel
	// Layout describes where wheels are installed in the image.
	Layout wheel.Layout
	// Strategy selects the dependency layering strategy.
	Strategy LayerStrategy

	// SourceDir is the application source directory (optional).
	SourceDir string
	// Ignore holds glob patterns (relative, slash-separated) to exclude from
	// the source layer, in addition to a built-in default set.
	Ignore []string

	// WorkingDir is the image working directory and app source destination.
	WorkingDir string
	// Entrypoint and Cmd configure the image.
	Entrypoint []string
	Cmd        []string
	// Env holds extra KEY=VALUE entries to set (in addition to the venv vars).
	Env []string
	// User sets the image user (e.g. "65532").
	User string
	// Labels are added to the image config.
	Labels map[string]string
}

var defaultIgnore = []string{
	".git", ".git/**",
	"**/__pycache__/**", "**/*.pyc", "**/*.pyo",
	".venv/**", "**/.pytest_cache/**", "**/.mypy_cache/**",
}

// Build assembles and returns the image. It performs no network I/O; callers
// push the result with remote.Write (see cmd) or write it locally.
func Build(opts Options) (v1.Image, error) {
	if opts.Base == nil {
		return nil, fmt.Errorf("build: Base image is required")
	}
	if opts.WorkingDir == "" {
		opts.WorkingDir = "/app"
	}
	if opts.Strategy == "" {
		opts.Strategy = PerWheel
	}

	adds, err := dependencyAddendums(opts)
	if err != nil {
		return nil, err
	}

	if opts.SourceDir != "" {
		appLayer, err := sourceLayer(opts.SourceDir, opts.WorkingDir, opts.Ignore)
		if err != nil {
			return nil, err
		}
		if appLayer != nil {
			adds = append(adds, mutate.Addendum{
				Layer:   appLayer,
				History: history("application source"),
			})
		}
	}

	img, err := mutate.Append(opts.Base, adds...)
	if err != nil {
		return nil, fmt.Errorf("build: append layers: %w", err)
	}

	return applyConfig(img, opts)
}

// dependencyAddendums builds the per-strategy dependency layer addendums.
func dependencyAddendums(opts Options) ([]mutate.Addendum, error) {
	switch opts.Strategy {
	case SingleDepsLayer:
		var all []ptar.File
		for _, rw := range opts.Wheels {
			files, err := wheelFiles(rw, opts.Layout)
			if err != nil {
				return nil, err
			}
			all = append(all, files...)
		}
		if len(all) == 0 {
			return nil, nil
		}
		layer, err := ptar.Layer(all)
		if err != nil {
			return nil, err
		}
		return []mutate.Addendum{{Layer: layer, History: history("python dependencies")}}, nil

	case PerWheel:
		adds := make([]mutate.Addendum, 0, len(opts.Wheels))
		for _, rw := range opts.Wheels {
			files, err := wheelFiles(rw, opts.Layout)
			if err != nil {
				return nil, err
			}
			layer, err := ptar.Layer(files)
			if err != nil {
				return nil, err
			}
			adds = append(adds, mutate.Addendum{
				Layer:   layer,
				History: history(fmt.Sprintf("wheel %s==%s", rw.Name, rw.Version)),
			})
		}
		return adds, nil

	default:
		return nil, fmt.Errorf("build: unknown layer strategy %q", opts.Strategy)
	}
}

func wheelFiles(rw wheelhouse.ResolvedWheel, layout wheel.Layout) ([]ptar.File, error) {
	w, err := wheel.Open(rw.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = w.Close() }()
	return w.Files(layout)
}

// sourceLayer builds a deterministic layer from the application source dir,
// placing files under workingDir. Returns nil if there is nothing to include.
func sourceLayer(srcDir, workingDir string, extraIgnore []string) (v1.Layer, error) {
	patterns := append(append([]string(nil), defaultIgnore...), extraIgnore...)
	dest := strings.TrimPrefix(path.Clean(workingDir), "/")

	var files []ptar.File
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if matchAny(patterns, rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks/sockets/etc for determinism
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, ptar.File{
			Path:       path.Join(dest, rel),
			Data:       data,
			Executable: info.Mode().Perm()&0o111 != 0,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("build: scan source: %w", err)
	}
	if len(files) == 0 {
		return nil, nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return ptar.Layer(files)
}

// matchAny reports whether rel matches any of the glob patterns. A pattern
// ending in "/**" matches the directory and everything under it.
func matchAny(patterns []string, rel string) bool {
	for _, pat := range patterns {
		if strings.HasSuffix(pat, "/**") {
			base := strings.TrimSuffix(pat, "/**")
			if rel == base || strings.HasPrefix(rel, base+"/") || pathMatch(base, rel) {
				return true
			}
			// also allow the prefix itself to be a glob like "**/__pycache__"
			if matchTail(base, rel) {
				return true
			}
			continue
		}
		if pathMatch(pat, rel) {
			return true
		}
		if matchTail(pat, rel) {
			return true
		}
	}
	return false
}

// pathMatch matches a single path against a glob (no '/' crossing for '*').
func pathMatch(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// matchTail handles "**/x" patterns by matching x against any path suffix.
func matchTail(pattern, rel string) bool {
	if !strings.HasPrefix(pattern, "**/") {
		return false
	}
	tail := strings.TrimPrefix(pattern, "**/")
	if pathMatch(tail, rel) {
		return true
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		if pathMatch(tail, strings.Join(parts[i:], "/")) {
			return true
		}
		if pathMatch(tail, parts[i]) {
			return true
		}
	}
	return false
}

// applyConfig rewrites the image config deterministically: venv env vars,
// entrypoint/cmd, user, labels, and zeroed timestamps.
func applyConfig(img v1.Image, opts Options) (v1.Image, error) {
	cf, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg := cf.DeepCopy()

	cfg.Created = v1.Time{Time: epoch()}
	cfg.Author = ""
	for i := range cfg.History {
		cfg.History[i].Created = v1.Time{Time: epoch()}
		cfg.History[i].Author = ""
	}

	c := &cfg.Config
	c.Env = mergeEnv(c.Env, opts.Layout, opts.Env)
	c.WorkingDir = opts.WorkingDir
	if len(opts.Entrypoint) > 0 {
		c.Entrypoint = opts.Entrypoint
		c.Cmd = opts.Cmd
	} else if len(opts.Cmd) > 0 {
		c.Cmd = opts.Cmd
	}
	if opts.User != "" {
		c.User = opts.User
	}
	if len(opts.Labels) > 0 {
		if c.Labels == nil {
			c.Labels = map[string]string{}
		}
		for k, v := range opts.Labels {
			c.Labels[k] = v
		}
	}

	return mutate.ConfigFile(img, cfg)
}

// mergeEnv layers the venv environment variables on top of the base env in a
// deterministic order. PATH gets the venv bin prepended; VIRTUAL_ENV and
// PYTHONPATH are set so imports and console scripts work regardless of the base
// interpreter location.
func mergeEnv(base []string, layout wheel.Layout, extra []string) []string {
	prefix := abs(layout.Prefix)
	bin := abs(layout.BinDir())
	site := abs(layout.SitePackages())

	env := map[string]string{}
	var order []string
	set := func(k, v string) {
		if _, ok := env[k]; !ok {
			order = append(order, k)
		}
		env[k] = v
	}
	for _, kv := range base {
		k, v, _ := strings.Cut(kv, "=")
		set(k, v)
	}

	if existing, ok := env["PATH"]; ok && existing != "" {
		set("PATH", bin+":"+existing)
	} else {
		set("PATH", bin+":/usr/local/bin:/usr/bin:/bin")
	}
	set("VIRTUAL_ENV", prefix)
	if existing, ok := env["PYTHONPATH"]; ok && existing != "" {
		set("PYTHONPATH", site+":"+existing)
	} else {
		set("PYTHONPATH", site)
	}

	for _, kv := range extra {
		k, v, _ := strings.Cut(kv, "=")
		set(k, v)
	}

	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+env[k])
	}
	return out
}

func history(createdBy string) v1.History {
	return v1.History{
		Created:   v1.Time{Time: epoch()},
		CreatedBy: "pymage: " + createdBy,
	}
}

func epoch() time.Time { return time.Unix(ptar.Epoch, 0).UTC() }

func abs(p string) string { return "/" + strings.TrimPrefix(path.Clean(p), "/") }
