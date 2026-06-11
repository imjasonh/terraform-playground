package wheelhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
)

// buildFromSdist builds a wheel from a requirement's source distribution and
// returns it as a ResolvedWheel. The built wheel is cached (keyed by the sdist
// hash and target) so it isn't rebuilt on subsequent builds.
//
// Building runs the host's `pip wheel`, so a Python toolchain must be present.
// A pure-python sdist yields a `py3-none-any` wheel usable on any target; a
// compiled sdist is built for the host platform only, so cross-arch builds of
// such packages will fail the compatibility check with a clear error.
func buildFromSdist(ctx context.Context, req lock.Requirement, target wheel.Target, cache *wheelCache) (ResolvedWheel, error) {
	if cache == nil {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: building %s==%s from sdist requires a cache dir (or use --no-cache)", req.Name, req.Version)
	}
	key := req.Sdist.SHA256 + "-" + targetKey(target)
	if p, ok := cache.getBuilt(key); ok {
		sum, err := fileSHA256(p)
		if err != nil {
			return ResolvedWheel{}, err
		}
		return ResolvedWheel{Name: req.Name, Version: req.Version, Path: p, SHA256: sum}, nil
	}

	work, err := os.MkdirTemp("", "pymage-sdist-")
	if err != nil {
		return ResolvedWheel{}, err
	}
	defer func() { _ = os.RemoveAll(work) }()

	sdistPath := filepath.Join(work, req.Sdist.Filename)
	if err := downloadVerify(ctx, req.Sdist.URL, req.Sdist.SHA256, sdistPath); err != nil {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: %s==%s sdist: %w", req.Name, req.Version, err)
	}

	out := filepath.Join(work, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return ResolvedWheel{}, err
	}
	if err := buildWheel(ctx, sdistPath, out); err != nil {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: build %s==%s from sdist: %w", req.Name, req.Version, err)
	}

	built, err := selectBuiltWheel(out, req, target)
	if err != nil {
		return ResolvedWheel{}, err
	}

	dst := cache.builtPath(key)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return ResolvedWheel{}, err
	}
	if err := os.Rename(built, dst); err != nil {
		if err := copyFile(built, dst); err != nil {
			return ResolvedWheel{}, err
		}
	}
	sum, err := fileSHA256(dst)
	if err != nil {
		return ResolvedWheel{}, err
	}
	return ResolvedWheel{Name: req.Name, Version: req.Version, Path: dst, SHA256: sum}, nil
}

func targetKey(t wheel.Target) string {
	return fmt.Sprintf("%s-%s-cp%d%d", t.OS, t.Arch, t.PyMajor, t.PyMinor)
}

func (c *wheelCache) builtPath(key string) string {
	return filepath.Join(c.dir, "built", key+".whl")
}

func (c *wheelCache) getBuilt(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	if _, err := os.Stat(c.builtPath(key)); err == nil {
		return c.builtPath(key), true
	}
	return "", false
}

// buildWheel builds a wheel from an sdist into outDir using `pip wheel`.
func buildWheel(ctx context.Context, sdist, outDir string) error {
	py := findPython()
	if py == "" {
		return fmt.Errorf("no python3/python on PATH to build the sdist (install Python+pip, supply a pre-built wheel via --find-links, or use a lock with wheels)")
	}
	cmd := exec.CommandContext(ctx, py, "-m", "pip", "wheel", "--no-deps", "--no-cache-dir", "--wheel-dir", outDir, sdist)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pip wheel: %w\n%s", err, tail(string(out), 20))
	}
	return nil
}

func findPython() string {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// selectBuiltWheel returns the built wheel in dir matching the requirement and
// compatible with the target.
func selectBuiltWheel(dir string, req lock.Requirement, target wheel.Target) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var compatible []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".whl") {
			continue
		}
		tags, err := wheel.ParseTags(e.Name())
		if err != nil {
			continue
		}
		if lock.NormalizeName(tags.Name) != lock.NormalizeName(req.Name) || tags.Version != req.Version {
			continue
		}
		if !tags.CompatibleWith(target) {
			continue
		}
		compatible = append(compatible, candidate{path: filepath.Join(dir, e.Name()), tags: tags})
	}
	if len(compatible) == 0 {
		return "", fmt.Errorf("wheelhouse: built wheel for %s==%s is not compatible with %s/%s python%d.%d (a compiled package can only be built for the host platform)",
			req.Name, req.Version, target.OS, target.Arch, target.PyMajor, target.PyMinor)
	}
	return pickBest(compatible).path, nil
}

func downloadVerify(ctx context.Context, url, wantSum, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSum) {
		return fmt.Errorf("sdist hash %s != expected %s", got, wantSum)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
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

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
