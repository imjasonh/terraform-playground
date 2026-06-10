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
	"hash/fnv"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"

	"github.com/imjasonh/terraform-playground/pymage/internal/cache"
	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/ptar"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheelhouse"
)

// layerFormatVersion is part of the cache key so a change in how we lay out
// wheels invalidates previously cached layers.
const layerFormatVersion = "1"

// LayerStrategy controls how dependency layers are partitioned.
type LayerStrategy string

const (
	// Auto keeps one layer per wheel while the total image layer count fits the
	// budget (see MaxLayers/MaxWheelLayers), and otherwise bin-packs wheels into
	// the budget by hashing each distribution name to a stable bucket — so a
	// single changed/added/removed dependency only perturbs one layer. (default)
	Auto LayerStrategy = "auto"
	// PerWheel always gives each wheel its own layer, ignoring any budget.
	PerWheel LayerStrategy = "per-wheel"
	// SingleDepsLayer collapses all wheels into one layer.
	SingleDepsLayer LayerStrategy = "single-deps-layer"
)

// DefaultMaxLayers is the default cap on the total number of image layers
// (base layers + dependency layers + the app source layer). 127 is the classic
// practical ceiling for container images.
const DefaultMaxLayers = 127

// Options configures an image build.
type Options struct {
	// Base is the resolved base image (use Base to resolve a reference).
	Base v1.Image
	// Wheels are the resolved dependency wheels, already in deterministic order.
	Wheels []wheelhouse.ResolvedWheel
	// Layout describes where wheels are installed in the image.
	Layout wheel.Layout
	// Strategy selects the dependency layering strategy (default Auto).
	Strategy LayerStrategy
	// MaxLayers caps the total image layer count (base + deps + app) for the
	// Auto strategy. <= 0 means DefaultMaxLayers (127).
	MaxLayers int
	// MaxWheelLayers, when > 0, caps the number of dependency layers directly
	// for the Auto strategy, taking precedence over MaxLayers.
	MaxWheelLayers int

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

	// Cache, if non-nil, stores/reuses built dependency layers across builds.
	Cache *cache.Cache
}

var defaultIgnore = []string{
	".git", ".git/**",
	"**/__pycache__/**", "**/*.pyc", "**/*.pyo",
	".venv/**", "**/.pytest_cache/**", "**/.mypy_cache/**",
	"uv.lock", "pyproject.toml", "wheelhouse", "wheelhouse/**",
	// Avoid baking common secret material into the image.
	"**/.env", "**/.env.*",
	"**/*.pem", "**/*.key", "**/*.pfx", "**/*.p12",
	"**/id_rsa", "**/id_dsa", "**/id_ecdsa", "**/id_ed25519",
	"**/.netrc",
	".ssh", ".ssh/**", ".aws", ".aws/**",
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
		opts.Strategy = Auto
	}

	baseLayers, err := imageLayerCount(opts.Base)
	if err != nil {
		return nil, fmt.Errorf("build: count base layers: %w", err)
	}

	adds, err := dependencyAddendums(opts, baseLayers)
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

// dependencyAddendums packs the wheels into layers (respecting the budget) and
// builds each group's layer.
func dependencyAddendums(opts Options, baseLayers int) ([]mutate.Addendum, error) {
	if len(opts.Wheels) == 0 {
		return nil, nil
	}
	budget, err := wheelBudget(opts, baseLayers)
	if err != nil {
		return nil, err
	}
	groups := packWheels(opts.Wheels, budget)

	layers, err := buildGroupLayers(groups, opts.Layout, opts.Cache)
	if err != nil {
		return nil, err
	}
	adds := make([]mutate.Addendum, len(groups))
	for i, g := range groups {
		adds[i] = mutate.Addendum{Layer: layers[i], History: history(groupDescription(g))}
	}
	return adds, nil
}

// wheelBudget returns the maximum number of dependency layers for the build.
func wheelBudget(opts Options, baseLayers int) (int, error) {
	switch opts.Strategy {
	case PerWheel:
		return len(opts.Wheels), nil // one per wheel, no cap
	case SingleDepsLayer:
		return 1, nil
	case Auto, "":
		if opts.MaxWheelLayers > 0 {
			return opts.MaxWheelLayers, nil
		}
		total := opts.MaxLayers
		if total <= 0 {
			total = DefaultMaxLayers
		}
		reserved := baseLayers
		if opts.SourceDir != "" {
			reserved++ // the app source layer also counts toward the total
		}
		budget := total - reserved
		if budget < 1 {
			// The base alone already meets/exceeds the cap; we can't shrink it,
			// so collapse all wheels into a single layer.
			budget = 1
		}
		return budget, nil
	default:
		return 0, fmt.Errorf("build: unknown layer strategy %q", opts.Strategy)
	}
}

// packWheels groups wheels into at most budget layers. While the wheels fit the
// budget each gets its own layer; otherwise wheels are assigned to buckets by a
// stable hash of their (normalized) distribution name. Because the bucket of a
// wheel depends only on its name, adding, removing, or version-bumping a single
// dependency changes only the one bucket that holds it — every other layer
// keeps its digest and is reused.
func packWheels(wheels []wheelhouse.ResolvedWheel, budget int) [][]wheelhouse.ResolvedWheel {
	if budget < 1 {
		budget = 1
	}
	if len(wheels) <= budget {
		groups := make([][]wheelhouse.ResolvedWheel, len(wheels))
		for i, w := range wheels {
			groups[i] = []wheelhouse.ResolvedWheel{w}
		}
		return groups
	}

	buckets := make([][]wheelhouse.ResolvedWheel, budget)
	for _, w := range wheels {
		b := bucketIndex(w.Name, budget)
		buckets[b] = append(buckets[b], w)
	}
	groups := make([][]wheelhouse.ResolvedWheel, 0, budget)
	for _, b := range buckets {
		if len(b) == 0 {
			continue
		}
		sortWheels(b)
		groups = append(groups, b)
	}
	return groups
}

// bucketIndex maps a distribution name to a stable bucket in [0, k). It uses
// FNV-1a (a fixed, version-stable algorithm) over the PEP 503 normalized name.
func bucketIndex(name string, k int) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(lock.NormalizeName(name)))
	return int(h.Sum64() % uint64(k))
}

func sortWheels(ws []wheelhouse.ResolvedWheel) {
	sort.Slice(ws, func(i, j int) bool {
		ni, nj := lock.NormalizeName(ws[i].Name), lock.NormalizeName(ws[j].Name)
		if ni != nj {
			return ni < nj
		}
		return ws[i].Version < ws[j].Version
	})
}

// buildGroupLayers builds each group's layer concurrently (bounded by CPU
// count), preserving group order.
func buildGroupLayers(groups [][]wheelhouse.ResolvedWheel, layout wheel.Layout, c *cache.Cache) ([]v1.Layer, error) {
	layers := make([]v1.Layer, len(groups))
	errs := make([]error, len(groups))

	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range groups {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			layers[i], errs[i] = buildGroupLayer(groups[i], layout, c)
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return layers, nil
}

// buildGroupLayer returns the layer for a group of wheels, using the cache when
// present.
func buildGroupLayer(group []wheelhouse.ResolvedWheel, layout wheel.Layout, c *cache.Cache) (v1.Layer, error) {
	key := groupCacheKey(group, layout)
	if c != nil {
		if l, ok := c.Get(key); ok {
			return l, nil
		}
	}
	var all []ptar.File
	for _, w := range group {
		files, err := wheelFiles(w, layout)
		if err != nil {
			return nil, err
		}
		all = append(all, files...)
	}
	layer, err := ptar.Layer(all)
	if err != nil {
		return nil, err
	}
	if c != nil {
		// A cache write failure must not fail the build; we just lose the
		// speedup for this layer.
		_ = c.Put(key, layer)
	}
	return layer, nil
}

// groupCacheKey is content-addressed by the (sorted) member wheel hashes and
// the install layout, so an identical group reuses its compressed blob.
func groupCacheKey(group []wheelhouse.ResolvedWheel, layout wheel.Layout) string {
	shas := make([]string, len(group))
	for i, w := range group {
		shas[i] = w.SHA256
	}
	sort.Strings(shas)
	return strings.Join([]string{
		"wheels", layerFormatVersion, layout.Prefix, layout.PythonTag, strings.Join(shas, ","),
	}, "|")
}

func groupDescription(group []wheelhouse.ResolvedWheel) string {
	if len(group) == 1 {
		return fmt.Sprintf("wheel %s==%s", group[0].Name, group[0].Version)
	}
	names := make([]string, len(group))
	for i, w := range group {
		names[i] = w.Name + "==" + w.Version
	}
	return fmt.Sprintf("%d wheels: %s", len(group), strings.Join(names, ", "))
}

// imageLayerCount returns the number of layers in img (no blob download).
func imageLayerCount(img v1.Image) (int, error) {
	ls, err := img.Layers()
	if err != nil {
		return 0, err
	}
	return len(ls), nil
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
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
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

	// PYTHONPATH is a path list, so we accumulate contributions (site-packages
	// first, then base, then extras such as the app's "/app/src") rather than
	// letting a later source overwrite it.
	pythonPath := []string{site}
	addPyPath := func(v string) {
		for _, p := range strings.Split(v, ":") {
			if p != "" {
				pythonPath = append(pythonPath, p)
			}
		}
	}
	apply := func(kv string) {
		k, v, _ := strings.Cut(kv, "=")
		if k == "PYTHONPATH" {
			addPyPath(v)
			return
		}
		set(k, v)
	}

	for _, kv := range base {
		apply(kv)
	}

	if existing, ok := env["PATH"]; ok && existing != "" {
		set("PATH", bin+":"+existing)
	} else {
		set("PATH", bin+":/usr/local/bin:/usr/bin:/bin")
	}
	set("VIRTUAL_ENV", prefix)

	for _, kv := range extra {
		apply(kv)
	}

	set("PYTHONPATH", strings.Join(dedupe(pythonPath), ":"))

	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+env[k])
	}
	return out
}

// dedupe returns s with duplicate entries removed, preserving first-seen order.
func dedupe(s []string) []string {
	seen := map[string]bool{}
	out := s[:0]
	for _, v := range s {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
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
