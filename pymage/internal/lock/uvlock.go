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
	Wheels       []uvArtifact       `toml:"wheels"`
	Dependencies []uvDep            `toml:"dependencies"`
	OptionalDeps map[string][]uvDep `toml:"optional-dependencies"`
	DevDeps      map[string][]uvDep `toml:"dev-dependencies"`
}

// uvDep is a dependency edge in uv.lock. Extras pull a package's
// optional-dependencies; dev-dependencies groups are never followed.
type uvDep struct {
	Name   string   `toml:"name"`
	Extras []string `toml:"extra"`
}

type uvArtifact struct {
	URL  string `toml:"url"`
	Hash string `toml:"hash"`
}

// ParseUVLockFile reads a uv.lock and returns the project's runtime dependency
// closure as pinned registry packages with wheel URLs and hashes.
//
// Only packages reachable from the local (project/workspace) packages' runtime
// dependencies — expanding requested extras via optional-dependencies — are
// included. Dev-dependency groups are excluded, matching `uv sync --no-dev`, so
// linters/test tools don't end up in the runtime image. Local packages
// themselves (editable/virtual/workspace) are skipped; application code comes
// from the source directory.
func ParseUVLockFile(path string) ([]Requirement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf uvLockFile
	if err := toml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("uv.lock: parse %q: %w", path, err)
	}

	include := runtimeClosure(lf.Package)

	var reqs []Requirement
	for _, pkg := range lf.Package {
		if isLocalSource(pkg.Source) {
			continue
		}
		// When a closure was computed, install only its members; otherwise
		// (no local/project package found) fall back to every package.
		if include != nil && !include[NormalizeName(pkg.Name)] {
			continue
		}
		if len(pkg.Wheels) == 0 {
			return nil, fmt.Errorf("uv.lock: %s==%s has no wheels (sdist-only deps are not supported)", pkg.Name, pkg.Version)
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
			req.Wheels = append(req.Wheels, WheelRef{
				URL:      w.URL,
				SHA256:   sum,
				Filename: filepath.Base(w.URL),
			})
		}
		reqs = append(reqs, req)
	}

	if len(reqs) == 0 {
		return nil, fmt.Errorf("uv.lock: no installable packages in %q", path)
	}
	return reqs, nil
}

// runtimeClosure returns the set of (normalized) package names reachable from
// the local packages' runtime dependencies, expanding extras. It returns nil
// when the lock has no local/project package (e.g. a bare requirements lock),
// signaling "install everything" for backward compatibility.
func runtimeClosure(pkgs []uvPackage) map[string]bool {
	byName := make(map[string]*uvPackage, len(pkgs))
	var roots []*uvPackage
	for i := range pkgs {
		p := &pkgs[i]
		byName[NormalizeName(p.Name)] = p
		if isLocalSource(p.Source) {
			roots = append(roots, p)
		}
	}
	if len(roots) == 0 {
		return nil
	}

	type item struct {
		name   string
		extras []string
	}
	var queue []item
	enqueue := func(deps []uvDep) {
		for _, d := range deps {
			queue = append(queue, item{NormalizeName(d.Name), d.Extras})
		}
	}
	for _, r := range roots {
		enqueue(r.Dependencies)
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
	return include
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
