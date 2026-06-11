package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type uvLockFile struct {
	Package []uvPackage `toml:"package"`
}

type uvPackage struct {
	Name         string             `toml:"name"`
	Version      string             `toml:"version"`
	Source       map[string]any     `toml:"source"`
	Sdist        uvArtifact         `toml:"sdist"`
	Wheels       []uvArtifact       `toml:"wheels"`
	Dependencies []uvDep            `toml:"dependencies"`
	OptionalDeps map[string][]uvDep `toml:"optional-dependencies"`
	DevDeps      map[string][]uvDep `toml:"dev-dependencies"`
}

// uvDep is a dependency edge in uv.lock. Extras pull a package's
// optional-dependencies; dev-dependencies groups are never followed. A marker
// gates the edge on the target environment.
type uvDep struct {
	Name   string   `toml:"name"`
	Extras []string `toml:"extra"`
	Marker string   `toml:"marker"`
}

type uvArtifact struct {
	URL  string `toml:"url"`
	Hash string `toml:"hash"`
}

// UVOptions selects which part of a uv.lock to install.
type UVOptions struct {
	// Package roots the closure at a single workspace member (by name). Empty
	// unions the closures of all local (project/workspace) packages.
	Package string
	// Extras enables the root project's own optional-dependency groups.
	Extras []string
	// Env, when non-nil, evaluates dependency markers for the target so
	// platform/python-gated deps are included only when they apply.
	Env MarkerEnv
}

// parseUVLock reads and unmarshals a uv.lock file.
func parseUVLock(path string) (*uvLockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf uvLockFile
	if err := toml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("uv.lock: parse %q: %w", path, err)
	}
	return &lf, nil
}

// ParseUVLockFile reads a uv.lock and returns its full runtime closure with
// default options (all local roots, no extras, no marker filtering). Prefer
// Load + Resolve to pass a package/extras/target.
func ParseUVLockFile(path string) ([]Requirement, error) {
	lf, err := parseUVLock(path)
	if err != nil {
		return nil, err
	}
	return resolveUV(lf, UVOptions{})
}

// resolveUV returns the project's runtime dependency closure as pinned
// packages with wheel URLs/hashes (and an sdist fallback when present).
//
// Only packages reachable from the local (project/workspace) packages' runtime
// dependencies — expanding requested extras via optional-dependencies and
// honoring markers — are included. Dev-dependency groups are excluded (like
// `uv sync --no-dev`). Local packages are skipped; app code comes from source.
func resolveUV(lf *uvLockFile, opts UVOptions) ([]Requirement, error) {
	include, err := runtimeClosure(lf.Package, opts)
	if err != nil {
		return nil, err
	}

	var reqs []Requirement
	for _, pkg := range lf.Package {
		if isLocalSource(pkg.Source) {
			continue
		}
		if include != nil && !include[NormalizeName(pkg.Name)] {
			continue
		}

		req := Requirement{Name: pkg.Name, Version: pkg.Version}
		seen := map[string]bool{}
		for _, w := range pkg.Wheels {
			sum, err := parseUVHash(w.Hash)
			if err != nil {
				return nil, fmt.Errorf("uv.lock: %s==%s: %w", pkg.Name, pkg.Version, err)
			}
			if w.URL == "" {
				return nil, fmt.Errorf("uv.lock: %s==%s: wheel missing url", pkg.Name, pkg.Version)
			}
			if seen[sum] {
				continue
			}
			seen[sum] = true
			req.Hashes = append(req.Hashes, sum)
			req.Wheels = append(req.Wheels, WheelRef{URL: w.URL, SHA256: sum, Filename: filepath.Base(w.URL)})
		}

		// An sdist provides a wheel-build fallback when no wheel is compatible.
		if pkg.Sdist.URL != "" {
			sum, err := parseUVHash(pkg.Sdist.Hash)
			if err != nil {
				return nil, fmt.Errorf("uv.lock: %s==%s sdist: %w", pkg.Name, pkg.Version, err)
			}
			req.Sdist = &SdistRef{URL: pkg.Sdist.URL, SHA256: sum, Filename: filepath.Base(pkg.Sdist.URL)}
			if !seen[sum] {
				req.Hashes = append(req.Hashes, sum) // counts as "hashed"
			}
		}

		if len(req.Wheels) == 0 && req.Sdist == nil {
			return nil, fmt.Errorf("uv.lock: %s==%s has neither wheels nor an sdist", pkg.Name, pkg.Version)
		}
		reqs = append(reqs, req)
	}

	if len(reqs) == 0 {
		return nil, fmt.Errorf("uv.lock: no installable packages (closure empty)")
	}
	return reqs, nil
}

// runtimeClosure returns the set of (normalized) package names reachable from
// the selected local packages' runtime dependencies, expanding extras and
// honoring markers. It returns nil when the lock has no local/project package
// (e.g. a bare requirements lock), signaling "install everything".
func runtimeClosure(pkgs []uvPackage, opts UVOptions) (map[string]bool, error) {
	byName := make(map[string]*uvPackage, len(pkgs))
	var localRoots []*uvPackage
	for i := range pkgs {
		p := &pkgs[i]
		byName[NormalizeName(p.Name)] = p
		if isLocalSource(p.Source) {
			localRoots = append(localRoots, p)
		}
	}

	var roots []*uvPackage
	if opts.Package != "" {
		p := byName[NormalizeName(opts.Package)]
		if p == nil {
			return nil, fmt.Errorf("uv.lock: --package %q not found in lock", opts.Package)
		}
		roots = []*uvPackage{p}
	} else {
		roots = localRoots
	}
	if len(roots) == 0 {
		return nil, nil
	}

	type item struct {
		name   string
		extras []string
	}
	var queue []item
	enqueue := func(deps []uvDep) {
		for _, d := range deps {
			if opts.Env != nil && !EvalMarker(d.Marker, opts.Env) {
				continue
			}
			queue = append(queue, item{NormalizeName(d.Name), d.Extras})
		}
	}
	for _, r := range roots {
		enqueue(r.Dependencies)
		// Enable the root project's own optional-dependency groups (--extra).
		for _, ex := range opts.Extras {
			enqueue(r.OptionalDeps[ex])
		}
	}

	include := map[string]bool{}
	visited := map[string]bool{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		key := it.name + "|" + strings.Join(it.extras, ",")
		if visited[key] {
			continue
		}
		visited[key] = true

		p := byName[it.name]
		if p == nil {
			continue
		}
		if !isLocalSource(p.Source) {
			include[it.name] = true
		}
		enqueue(p.Dependencies)
		for _, ex := range it.extras {
			enqueue(p.OptionalDeps[ex])
		}
	}
	return include, nil
}

func isLocalSource(src map[string]any) bool {
	if src == nil {
		return false
	}
	for _, k := range []string{"virtual", "editable", "workspace", "directory", "git"} {
		if _, ok := src[k]; ok {
			return true
		}
	}
	return false
}

func parseUVHash(h string) (string, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return "", fmt.Errorf("missing hash")
	}
	const prefix = "sha256:"
	if !strings.HasPrefix(strings.ToLower(h), prefix) {
		return "", fmt.Errorf("unsupported hash %q (expected sha256:...)", h)
	}
	sum := strings.ToLower(strings.TrimPrefix(strings.ToLower(h), prefix))
	if len(sum) != 64 {
		return "", fmt.Errorf("bad sha256 length in %q", h)
	}
	return sum, nil
}
