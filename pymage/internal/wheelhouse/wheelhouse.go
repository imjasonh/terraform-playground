// Package wheelhouse resolves pinned requirements to concrete wheel files held
// in one or more local directories ("find-links" style), filtering by the
// target platform/interpreter and verifying that each file's sha256 matches the
// lock.
//
// Keeping resolution local keeps builds hermetic and reproducible: the lock +
// the wheelhouse fully determine the output. (A network fetcher that downloads
// wheels by URL/sha256 is a straightforward future addition that feeds the same
// ResolvedWheel path.)
package wheelhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
)

// ResolvedWheel pairs a pinned requirement with the wheel file that satisfies
// it and that file's content hash.
type ResolvedWheel struct {
	Name    string
	Version string
	Path    string
	SHA256  string // hex
}

// candidate is a wheel file discovered in the wheelhouse.
type candidate struct {
	path    string
	name    string // normalized
	version string
	tags    wheel.Tags
}

// Resolve matches each requirement to a wheel file found in dirs that is
// compatible with target, verifying its hash. When dirs are empty or miss a
// package, wheel URLs embedded in the lock are downloaded into wheelCacheDir.
// Results are sorted by (normalized name, version) for deterministic ordering.
func Resolve(reqs []lock.Requirement, dirs []string, target wheel.Target) ([]ResolvedWheel, error) {
	return ResolveContext(context.Background(), reqs, dirs, target, "")
}

// ResolveContext is Resolve with an explicit context and on-disk wheel cache
// directory for lock-file downloads.
func ResolveContext(ctx context.Context, reqs []lock.Requirement, dirs []string, target wheel.Target, wheelCacheDir string) ([]ResolvedWheel, error) {
	var index map[string][]candidate
	if len(dirs) > 0 {
		var err error
		index, err = scan(dirs)
		if err != nil {
			return nil, err
		}
	}

	cache, err := openWheelCache(wheelCacheDir)
	if err != nil {
		return nil, err
	}

	out := make([]ResolvedWheel, 0, len(reqs))
	for _, req := range reqs {
		rw, err := resolveOne(ctx, req, index, target, cache)
		if err != nil {
			return nil, err
		}
		out = append(out, rw)
	}

	sort.Slice(out, func(i, j int) bool {
		ni, nj := lock.NormalizeName(out[i].Name), lock.NormalizeName(out[j].Name)
		if ni != nj {
			return ni < nj
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func resolveOne(ctx context.Context, req lock.Requirement, index map[string][]candidate, target wheel.Target, cache *wheelCache) (ResolvedWheel, error) {
	key := lock.NormalizeName(req.Name) + "\x00" + req.Version
	if index != nil {
		if cands := index[key]; len(cands) > 0 {
			compatible := make([]candidate, 0, len(cands))
			for _, c := range cands {
				if c.tags.CompatibleWith(target) {
					compatible = append(compatible, c)
				}
			}
			if len(compatible) > 0 {
				match := pickBest(compatible)
				sum, err := hashFile(match.path)
				if err != nil {
					return ResolvedWheel{}, err
				}
				if len(req.Hashes) > 0 && !containsHash(req.Hashes, sum) {
					return ResolvedWheel{}, fmt.Errorf("wheelhouse: %s==%s hash mismatch: file %s has sha256:%s, not in lock", req.Name, req.Version, match.path, sum)
				}
				return ResolvedWheel{Name: req.Name, Version: req.Version, Path: match.path, SHA256: sum}, nil
			}
			if len(req.Wheels) == 0 {
				return ResolvedWheel{}, fmt.Errorf("wheelhouse: no wheel for %s==%s is compatible with %s/%s python%d.%d (found %d incompatible candidate(s))",
					req.Name, req.Version, target.OS, target.Arch, target.PyMajor, target.PyMinor, len(cands))
			}
		}
	}

	if len(req.Wheels) == 0 {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: no wheel found for %s==%s (pass --find-links or use a uv.lock with wheel URLs)", req.Name, req.Version)
	}
	if cache == nil {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: %s==%s not in local wheelhouse and no wheel cache dir configured", req.Name, req.Version)
	}
	return fetchRequirement(ctx, req, target, cache)
}

// pickBest chooses the most specific compatible wheel: platform-specific and
// ABI-specific wheels are preferred over pure-python ones, with the filename as
// a deterministic tie-breaker.
func pickBest(cands []candidate) candidate {
	sort.Slice(cands, func(i, j int) bool {
		si, sj := specificity(cands[i].tags), specificity(cands[j].tags)
		if si != sj {
			return si > sj
		}
		// Prefer the highest build tag (PEP 427 build-tag tie-break).
		bi, bj := cands[i].tags.BuildRank(), cands[j].tags.BuildRank()
		if bi != bj {
			return bi > bj
		}
		return cands[i].path < cands[j].path
	})
	return cands[0]
}

func specificity(t wheel.Tags) int {
	s := 0
	if t.Platform != "any" {
		s += 2
	}
	if t.ABI != "none" {
		s++
	}
	return s
}

func scan(dirs []string) (map[string][]candidate, error) {
	index := map[string][]candidate{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("wheelhouse: read dir %q: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".whl") {
				continue
			}
			tags, err := wheel.ParseTags(e.Name())
			if err != nil {
				continue
			}
			name := lock.NormalizeName(tags.Name)
			key := name + "\x00" + tags.Version
			index[key] = append(index[key], candidate{
				path:    filepath.Join(dir, e.Name()),
				name:    name,
				version: tags.Version,
				tags:    tags,
			})
		}
	}
	return index, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func containsHash(hashes []string, sum string) bool {
	for _, h := range hashes {
		if strings.EqualFold(h, sum) {
			return true
		}
	}
	return false
}
