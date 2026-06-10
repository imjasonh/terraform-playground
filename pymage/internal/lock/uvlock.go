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
	Name   string        `toml:"name"`
	Version string       `toml:"version"`
	Source map[string]any `toml:"source"`
	Wheels []uvArtifact  `toml:"wheels"`
}

type uvArtifact struct {
	URL  string `toml:"url"`
	Hash string `toml:"hash"`
}

// ParseUVLockFile reads a uv.lock and returns pinned registry packages with
// wheel URLs and hashes. Local workspace packages (editable, virtual, etc.)
// are skipped — application code comes from the source directory instead.
func ParseUVLockFile(path string) ([]Requirement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf uvLockFile
	if err := toml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("uv.lock: parse %q: %w", path, err)
	}

	var reqs []Requirement
	for _, pkg := range lf.Package {
		if isLocalSource(pkg.Source) {
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
