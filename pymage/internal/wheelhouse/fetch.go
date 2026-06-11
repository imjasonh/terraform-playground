package wheelhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
)

// wheelCache stores downloaded .whl files keyed by sha256.
type wheelCache struct {
	dir string
}

func openWheelCache(dir string) (*wheelCache, error) {
	if dir == "" {
		return nil, nil
	}
	// The directory is created lazily on first download (see put), so resolving
	// entirely from a local wheelhouse never touches the filesystem.
	return &wheelCache{dir: dir}, nil
}

func (c *wheelCache) path(sum string) string {
	return filepath.Join(c.dir, sum+".whl")
}

func (c *wheelCache) get(sum string) (string, bool) {
	if c == nil {
		return "", false
	}
	p := c.path(sum)
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

func (c *wheelCache) put(sum string, r io.Reader) (string, error) {
	if c == nil {
		return "", fmt.Errorf("no wheel cache configured")
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return "", fmt.Errorf("wheel cache: %w", err)
	}
	tmp, err := os.CreateTemp(c.dir, ".tmp-")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), r); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, sum) {
		return "", fmt.Errorf("downloaded wheel hash %s != expected %s", got, sum)
	}
	dest := c.path(sum)
	if err := os.Rename(tmpName, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func downloadWheel(ctx context.Context, url, wantSum string, cache *wheelCache) (string, error) {
	if p, ok := cache.get(wantSum); ok {
		return p, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}
	return cache.put(wantSum, resp.Body)
}

func pickLockWheel(req lock.Requirement, target wheel.Target) (lock.WheelRef, error) {
	if len(req.Wheels) == 0 {
		return lock.WheelRef{}, fmt.Errorf("no wheel URLs in lock for %s==%s", req.Name, req.Version)
	}
	type tagged struct {
		ref  lock.WheelRef
		tags wheel.Tags
	}
	var cands []tagged
	for _, w := range req.Wheels {
		tags, err := wheel.ParseTags(w.Filename)
		if err != nil {
			continue
		}
		if tags.Name != "" && lock.NormalizeName(tags.Name) != lock.NormalizeName(req.Name) {
			continue
		}
		if tags.Version != "" && tags.Version != req.Version {
			continue
		}
		if !tags.CompatibleWith(target) {
			continue
		}
		cands = append(cands, tagged{ref: w, tags: tags})
	}
	if len(cands) == 0 {
		return lock.WheelRef{}, fmt.Errorf("no lock wheel for %s==%s is compatible with %s/%s python%d.%d",
			req.Name, req.Version, target.OS, target.Arch, target.PyMajor, target.PyMinor)
	}
	// Reuse pickBest logic via candidate conversion.
	wcands := make([]candidate, len(cands))
	for i, c := range cands {
		wcands[i] = candidate{path: c.ref.Filename, tags: c.tags}
	}
	best := pickBest(wcands)
	for _, c := range cands {
		if c.ref.Filename == best.path {
			return c.ref, nil
		}
	}
	return cands[0].ref, nil
}

func fetchRequirement(ctx context.Context, req lock.Requirement, target wheel.Target, cache *wheelCache) (ResolvedWheel, error) {
	wref, err := pickLockWheel(req, target)
	if err != nil {
		return ResolvedWheel{}, err
	}
	if len(req.Hashes) > 0 && !containsHash(req.Hashes, wref.SHA256) {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: selected wheel hash %s not allowed for %s==%s", wref.SHA256, req.Name, req.Version)
	}
	path, err := downloadWheel(ctx, wref.URL, wref.SHA256, cache)
	if err != nil {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: %s==%s: %w", req.Name, req.Version, err)
	}
	return ResolvedWheel{Name: req.Name, Version: req.Version, Path: path, SHA256: wref.SHA256}, nil
}
