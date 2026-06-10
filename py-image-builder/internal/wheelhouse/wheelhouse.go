// Package wheelhouse resolves pinned requirements to concrete wheel files held
// in one or more local directories ("find-links" style), verifying that each
// file's sha256 matches the lock.
//
// Keeping resolution local keeps builds hermetic and reproducible: the lock +
// the wheelhouse fully determine the output. (A network fetcher that downloads
// wheels by URL/sha256 is a straightforward future addition that feeds the same
// ResolvedWheel path.)
package wheelhouse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imjasonh/terraform-playground/py-image-builder/internal/lock"
	"github.com/imjasonh/terraform-playground/py-image-builder/internal/wheel"
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
}

// Resolve matches each requirement to a wheel file found in dirs. It returns
// resolved wheels sorted by (normalized name, version) for deterministic layer
// ordering.
func Resolve(reqs []lock.Requirement, dirs []string) ([]ResolvedWheel, error) {
	cands, err := scan(dirs)
	if err != nil {
		return nil, err
	}

	out := make([]ResolvedWheel, 0, len(reqs))
	for _, req := range reqs {
		want := lock.NormalizeName(req.Name)
		var match *candidate
		for i := range cands {
			c := &cands[i]
			if c.name == want && c.version == req.Version {
				match = c
				break
			}
		}
		if match == nil {
			return nil, fmt.Errorf("wheelhouse: no wheel found for %s==%s in %v", req.Name, req.Version, dirs)
		}
		sum, err := hashFile(match.path)
		if err != nil {
			return nil, err
		}
		if len(req.Hashes) > 0 && !containsHash(req.Hashes, sum) {
			return nil, fmt.Errorf("wheelhouse: %s==%s hash mismatch: file %s has sha256:%s, not in lock", req.Name, req.Version, match.path, sum)
		}
		out = append(out, ResolvedWheel{Name: req.Name, Version: req.Version, Path: match.path, SHA256: sum})
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

func scan(dirs []string) ([]candidate, error) {
	var cands []candidate
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("wheelhouse: read dir %q: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".whl") {
				continue
			}
			name, version, err := wheel.ParseFilename(e.Name())
			if err != nil {
				continue
			}
			cands = append(cands, candidate{
				path:    filepath.Join(dir, e.Name()),
				name:    lock.NormalizeName(name),
				version: version,
			})
		}
	}
	return cands, nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func containsHash(hashes []string, sum string) bool {
	for _, h := range hashes {
		if strings.EqualFold(h, sum) {
			return true
		}
	}
	return false
}
