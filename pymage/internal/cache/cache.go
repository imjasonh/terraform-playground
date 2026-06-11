// Package cache is a content-addressed, on-disk store for built OCI layers.
//
// Building a layer from a wheel means reading every member and gzip-compressing
// the tar — pure CPU that is wasted on every rebuild for dependencies that have
// not changed. Keyed by the wheel's sha256 (plus the install layout), the cache
// lets an unchanged dependency reuse its previously compressed blob verbatim,
// so a rebuild does no decompression/recompression for those layers.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// Cache stores compressed layer blobs under a directory.
type Cache struct {
	dir string
}

// New returns a cache rooted at dir, creating it if necessary.
func New(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache: create %q: %w", dir, err)
	}
	return &Cache{dir: dir}, nil
}

func (c *Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".tar.gz")
}

// Get returns the cached layer for key, or (nil, false) on a miss.
func (c *Cache) Get(key string) (v1.Layer, bool) {
	p := c.path(key)
	if _, err := os.Stat(p); err != nil {
		return nil, false
	}
	l, err := tarball.LayerFromFile(p)
	if err != nil {
		return nil, false
	}
	return l, true
}

// metaPath returns the on-disk path for a small text value keyed by key.
func (c *Cache) metaPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, "meta", hex.EncodeToString(sum[:])+".txt")
}

// GetText returns a small cached text value for key (e.g. a detected base
// interpreter version), or ("", false) on a miss.
func (c *Cache) GetText(key string) (string, bool) {
	data, err := os.ReadFile(c.metaPath(key))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// PutText stores a small text value for key.
func (c *Cache) PutText(key, val string) error {
	p := c.metaPath(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(val); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, p)
}

// Put stores the layer's compressed blob under key. Writes are atomic (temp
// file + rename) so concurrent builders and interrupted writes are safe.
func (c *Cache) Put(key string, l v1.Layer) error {
	rc, err := l.Compressed()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, c.path(key))
}

// replaceFile atomically moves src onto dst. os.Rename replaces an existing
// dst on POSIX but fails on Windows, so retry after removing dst there. The
// store is content-addressed (or overwrites an equivalent value), so removing
// an existing dst is safe.
func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		if _, statErr := os.Stat(dst); statErr == nil {
			_ = os.Remove(dst)
			return os.Rename(src, dst)
		}
		return err
	}
	return nil
}
