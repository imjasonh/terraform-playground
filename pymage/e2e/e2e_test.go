// Package e2e exercises pymage end to end against a real local OCI
// registry (go-containerregistry's in-process registry served over HTTP — a
// genuine registry daemon, just in the test process). No Docker is required.
//
// These tests demonstrate the two headline properties:
//
//   - Reproducibility: identical inputs produce an identical image digest.
//   - No-bytes rebuild: re-pushing an unchanged image transfers zero blob
//     bytes, and adding one new dependency uploads exactly one new dependency
//     layer (existing dependency layers are never re-uploaded).
package e2e

import (
	"archive/tar"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/imjasonh/terraform-playground/pymage/internal/build"
	"github.com/imjasonh/terraform-playground/pymage/internal/testwheel"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheelhouse"
)

// uploadCounter records the set of blob digests that were actually uploaded
// (i.e. transferred bytes). Blobs that the registry already has are HEAD-
// deduped by the client and never reach a finalize request, so they don't show
// up here.
type uploadCounter struct {
	mu       sync.Mutex
	uploaded map[string]bool
}

func newCounter() *uploadCounter { return &uploadCounter{uploaded: map[string]bool{}} }

func (c *uploadCounter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.uploaded = map[string]bool{}
}

func (c *uploadCounter) snapshot() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]bool, len(c.uploaded))
	for k := range c.uploaded {
		out[k] = true
	}
	return out
}

func (c *uploadCounter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/blobs/uploads/") &&
			(r.Method == http.MethodPut || r.Method == http.MethodPost) {
			if d := r.URL.Query().Get("digest"); d != "" {
				c.mu.Lock()
				c.uploaded[d] = true
				c.mu.Unlock()
			}
		}
		next.ServeHTTP(w, r)
	})
}

var layout = wheel.Layout{Prefix: "/app/.venv", PythonTag: "python3.12"}

func mkWheel(t *testing.T, dir, name, version string) wheelhouse.ResolvedWheel {
	t.Helper()
	path, sha := testwheel.Write(t, dir, testwheel.Spec{
		Name:    name,
		Version: version,
		Modules: map[string]string{name + "/__init__.py": "V = '" + version + "'\n"},
	})
	return wheelhouse.ResolvedWheel{Name: name, Version: version, Path: path, SHA256: sha}
}

func startRegistry(t *testing.T) (host string, c *uploadCounter) {
	t.Helper()
	c = newCounter()
	s := httptest.NewServer(c.middleware(registry.New()))
	t.Cleanup(s.Close)
	return strings.TrimPrefix(s.URL, "http://"), c
}

func push(t *testing.T, ref string, img v1.Image) {
	t.Helper()
	r, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(r, img, remote.WithContext(context.Background())); err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
}

func pullDigest(t *testing.T, ref string) v1.Hash {
	t.Helper()
	r, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	img, err := remote.Image(r, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	d, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func appOptions(base v1.Image, wheels []wheelhouse.ResolvedWheel) build.Options {
	return build.Options{
		Base:       base,
		Wheels:     wheels,
		Layout:     layout,
		Strategy:   build.PerWheel,
		WorkingDir: "/app",
		Entrypoint: []string{"python", "-m", "app"},
	}
}

func layerDigests(t *testing.T, img v1.Image) []string {
	t.Helper()
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range layers {
		d, err := l.Digest()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, d.String())
	}
	return out
}

// TestE2EReproducibleAndNoBytesRebuild is the core demonstration.
func TestE2EReproducibleAndNoBytesRebuild(t *testing.T) {
	host, counter := startRegistry(t)
	ctx := context.Background()

	// Push an (empty) base image and resolve it back by reference, exercising
	// the docker-less base-by-reference path.
	baseRef := host + "/base:latest"
	push(t, baseRef, empty.Image)
	base, err := build.Base(ctx, baseRef, nil, nil)
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}

	dir := t.TempDir()
	a := mkWheel(t, dir, "alpha", "1.0")
	b := mkWheel(t, dir, "beta", "2.0")
	c := mkWheel(t, dir, "gamma", "3.0")

	// --- Build #1: deps [alpha, beta], push to app:v1.
	img1, err := build.Build(appOptions(base, []wheelhouse.ResolvedWheel{a, b}))
	if err != nil {
		t.Fatal(err)
	}
	d1, _ := img1.Digest()

	counter.reset()
	push(t, host+"/app:v1", img1)
	firstUploads := counter.snapshot()
	if len(firstUploads) == 0 {
		t.Fatal("expected the first push to upload blobs")
	}

	// --- Build #2: identical inputs, push to app:v2 (same repo).
	img2, err := build.Build(appOptions(base, []wheelhouse.ResolvedWheel{a, b}))
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := img2.Digest()

	// Reproducibility: identical image digest across independent builds.
	if d1 != d2 {
		t.Fatalf("build not reproducible: %s != %s", d1, d2)
	}

	counter.reset()
	push(t, host+"/app:v2", img2)
	if up := counter.snapshot(); len(up) != 0 {
		t.Fatalf("no-bytes rebuild violated: re-push uploaded %d blob(s): %v", len(up), up)
	}

	// Pulled digests for both tags match the built digest.
	if got := pullDigest(t, host+"/app:v1"); got != d1 {
		t.Fatalf("pulled v1 digest %s != built %s", got, d1)
	}
	if got := pullDigest(t, host+"/app:v2"); got != d1 {
		t.Fatalf("pulled v2 digest %s != built %s", got, d1)
	}

	// --- Build #3: add ONE new dep [alpha, beta, gamma]; push to app:v3.
	img3, err := build.Build(appOptions(base, []wheelhouse.ResolvedWheel{a, b, c}))
	if err != nil {
		t.Fatal(err)
	}

	// Identify each wheel's layer digest by building the layers standalone.
	wantUnchanged := map[string]bool{
		soleLayerDigest(t, a): true,
		soleLayerDigest(t, b): true,
	}
	gammaLayer := soleLayerDigest(t, c)

	// Sanity: the new image actually contains the unchanged layers + gamma.
	have := map[string]bool{}
	for _, d := range layerDigests(t, img3) {
		have[d] = true
	}
	for d := range wantUnchanged {
		if !have[d] {
			t.Fatalf("expected unchanged layer %s to be present in new image", d)
		}
	}
	if !have[gammaLayer] {
		t.Fatalf("expected new gamma layer %s in new image", gammaLayer)
	}

	counter.reset()
	push(t, host+"/app:v3", img3)
	uploaded := counter.snapshot()

	// The previously-pushed dependency layers must NOT be re-uploaded.
	for d := range wantUnchanged {
		if uploaded[d] {
			t.Errorf("existing dependency layer %s was re-uploaded; only new deps should upload", d)
		}
	}
	// The new dependency layer MUST be uploaded.
	if !uploaded[gammaLayer] {
		t.Errorf("new dependency layer %s was not uploaded", gammaLayer)
	}
}

// TestE2EInstalledPackagesImportable flattens the built image's filesystem and
// imports the installed package with the real interpreter, proving the wheel
// install layout is correct (not just that digests are stable).
func TestE2EInstalledPackagesImportable(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	tag := pythonTag(t, py)
	lay := wheel.Layout{Prefix: "/app/.venv", PythonTag: tag}

	dir := t.TempDir()
	a := mkWheel(t, dir, "alpha", "1.0")

	img, err := build.Build(build.Options{
		Base:       empty.Image,
		Wheels:     []wheelhouse.ResolvedWheel{a},
		Layout:     lay,
		Strategy:   build.PerWheel,
		WorkingDir: "/app",
	})
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	extract(t, img, root)

	site := filepath.Join(root, "app/.venv/lib", tag, "site-packages")
	cmd := exec.Command(py, "-c", "import alpha; print(alpha.V)")
	cmd.Env = append(os.Environ(), "PYTHONPATH="+site)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("import failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "1.0" {
		t.Fatalf("unexpected import output: %q", out)
	}
}

func pythonTag(t *testing.T, py string) string {
	t.Helper()
	out, err := exec.Command(py, "-c", "import sys;print('python%d.%d'%sys.version_info[:2])").Output()
	if err != nil {
		t.Fatalf("python version: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func extract(t *testing.T, img v1.Image, dest string) {
	t.Helper()
	rc := mutate.Extract(img)
	defer func() { _ = rc.Close() }()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dest, filepath.Clean("/"+h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			_ = f.Close()
		}
	}
}

// soleLayerDigest builds the wheel as a standalone single-layer image and
// returns that layer's digest, matching what Build produces per wheel.
func soleLayerDigest(t *testing.T, rw wheelhouse.ResolvedWheel) string {
	t.Helper()
	img, err := build.Build(build.Options{
		Base:     empty.Image,
		Wheels:   []wheelhouse.ResolvedWheel{rw},
		Layout:   layout,
		Strategy: build.PerWheel,
	})
	if err != nil {
		t.Fatal(err)
	}
	ds := layerDigests(t, img)
	if len(ds) != 1 {
		t.Fatalf("expected 1 layer for %s, got %d", rw.Name, len(ds))
	}
	return ds[0]
}
