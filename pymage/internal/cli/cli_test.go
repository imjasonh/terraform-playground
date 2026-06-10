package cli

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/imjasonh/terraform-playground/pymage/internal/testwheel"
)

// TestBuildMultiArchIndex drives the CLI to build a linux/amd64 + linux/arm64
// image index from a single host, and verifies the pushed artifact is an index
// covering both platforms and is reproducible.
func TestBuildMultiArchIndex(t *testing.T) {
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")

	// A multi-arch base (linux/amd64 + linux/arm64), as a real registry would
	// serve python:3.12-slim.
	baseRef := host + "/base:latest"
	writeMultiArchBase(t, baseRef, []string{"amd64", "arm64"})

	// Pure-python wheels are compatible with every platform.
	wh := t.TempDir()
	_, shaA := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1.0'\n"}})
	reqDir := t.TempDir()
	reqFile := filepath.Join(reqDir, "requirements.txt")
	if err := os.WriteFile(reqFile, []byte(fmt.Sprintf("alpha==1.0 --hash=sha256:%s\n", shaA)), 0o644); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "app.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	build := func(tag string) error {
		cmd := Root()
		cmd.SetArgs([]string{
			"build",
			"--base", baseRef,
			"--lock", reqFile,
			"--find-links", wh,
			"--source", src,
			"--platform", "linux/amd64,linux/arm64",
			"--entrypoint", "python", "--entrypoint", "/app/app.py",
			"-t", tag,
		})
		cmd.SetOut(os.Stderr)
		return cmd.Execute()
	}

	if err := build(host + "/multi:v1"); err != nil {
		t.Fatalf("multi-arch build failed: %v", err)
	}

	ref, _ := name.ParseReference(host + "/multi:v1")
	idx, err := remote.Index(ref, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatalf("pulled artifact is not an index: %v", err)
	}
	im, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range im.Manifests {
		if m.Platform != nil {
			got[m.Platform.OS+"/"+m.Platform.Architecture] = true
		}
	}
	for _, want := range []string{"linux/amd64", "linux/arm64"} {
		if !got[want] {
			t.Errorf("index missing platform %s; got %v", want, got)
		}
	}
	if len(im.Manifests) != 2 {
		t.Fatalf("expected 2 manifests, got %d", len(im.Manifests))
	}
	d1, _ := idx.Digest()

	// Reproducible: a second build yields the same index digest.
	if err := build(host + "/multi:v2"); err != nil {
		t.Fatal(err)
	}
	ref2, _ := name.ParseReference(host + "/multi:v2")
	idx2, err := remote.Index(ref2, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := idx2.Digest()
	if d1 != d2 {
		t.Fatalf("multi-arch index not reproducible: %s != %s", d1, d2)
	}
}

func writeMultiArchBase(t *testing.T, ref string, arches []string) {
	t.Helper()
	var adds []mutate.IndexAddendum
	for _, arch := range arches {
		img, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{
			OS:           "linux",
			Architecture: arch,
			Config:       v1.Config{Env: []string{"PATH=/usr/local/bin:/usr/bin:/bin"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		adds = append(adds, mutate.IndexAddendum{
			Add:        img,
			Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: arch}},
		})
	}
	idx := mutate.AppendManifests(empty.Index, adds...)
	r, _ := name.ParseReference(ref)
	if err := remote.WriteIndex(r, idx, remote.WithContext(context.Background())); err != nil {
		t.Fatalf("write base index: %v", err)
	}
}

// TestBuildRejectsInterpreterMismatch ensures a base advertising a different
// Python version than --python fails fast (the floating-tag drift guard).
func TestBuildRejectsInterpreterMismatch(t *testing.T) {
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")

	baseRef := host + "/base:py313"
	img, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{
		OS: "linux", Architecture: "amd64",
		Config: v1.Config{Env: []string{"PYTHON_VERSION=3.13.1", "PATH=/usr/bin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := name.ParseReference(baseRef)
	if err := remote.Write(r, img, remote.WithContext(context.Background())); err != nil {
		t.Fatal(err)
	}

	wh := t.TempDir()
	_, sha := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1'\n"}})
	reqFile := filepath.Join(t.TempDir(), "requirements.txt")
	if err := os.WriteFile(reqFile, []byte(fmt.Sprintf("alpha==1.0 --hash=sha256:%s\n", sha)), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := Root()
	cmd.SetArgs([]string{
		"build",
		"--base", baseRef,
		"--lock", reqFile,
		"--find-links", wh,
		"--python", "python3.12", // mismatches the base's 3.13
		"--push=false",
		"--print-digest",
	})
	cmd.SetOut(os.Stderr)
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected build to fail on interpreter mismatch")
	}
	if !strings.Contains(err.Error(), "Python 3.13") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePythonTag(t *testing.T) {
	maj, min, err := parsePythonTag("python3.12")
	if err != nil || maj != 3 || min != 12 {
		t.Fatalf("parsePythonTag = %d.%d, %v", maj, min, err)
	}
	if _, _, err := parsePythonTag("python3"); err == nil {
		t.Error("expected error for missing minor version")
	}
}

func TestResolveTargetDefaults(t *testing.T) {
	tg, err := resolveTarget(nil, "python3.11")
	if err != nil {
		t.Fatal(err)
	}
	if tg.OS != "linux" || tg.Arch != "amd64" || tg.PyMajor != 3 || tg.PyMinor != 11 {
		t.Fatalf("unexpected target %+v", tg)
	}
}

func TestEnvValidation(t *testing.T) {
	if _, err := keyValues([]string{"GOOD=1"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := keyValues([]string{"NOEQUALS"}); err == nil {
		t.Error("expected error for env entry without '='")
	}
	if _, err := keyValues([]string{"=value"}); err == nil {
		t.Error("expected error for env entry with empty key")
	}
}

// TestBuildCommandEndToEnd drives the cobra `build` command exactly as a user
// would: a hashed requirements file + a wheelhouse + source, pushed to a local
// registry, then verifies the pushed image is pullable and reproducible.
func TestBuildCommandEndToEnd(t *testing.T) {
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://") // 127.0.0.1:port -> ggcr uses http

	// Seed a base image.
	baseRef := host + "/base:latest"
	br, err := name.ParseReference(baseRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(br, empty.Image, remote.WithContext(context.Background())); err != nil {
		t.Fatal(err)
	}

	// Wheelhouse with two wheels.
	wh := t.TempDir()
	_, shaA := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1.0'\n"}})
	_, shaB := testwheel.Write(t, wh, testwheel.Spec{Name: "beta", Version: "2.0", Modules: map[string]string{"beta/__init__.py": "V='2.0'\n"}})

	// Hashed requirements file.
	reqDir := t.TempDir()
	reqFile := filepath.Join(reqDir, "requirements.txt")
	reqs := fmt.Sprintf("alpha==1.0 --hash=sha256:%s\nbeta==2.0 --hash=sha256:%s\n", shaA, shaB)
	if err := os.WriteFile(reqFile, []byte(reqs), 0o644); err != nil {
		t.Fatal(err)
	}

	// App source.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "app.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tag := host + "/myapp:v1"
	run := func() error {
		cmd := Root()
		cmd.SetArgs([]string{
			"build",
			"--base", baseRef,
			"--lock", reqFile,
			"--find-links", wh,
			"--source", src,
			"--entrypoint", "python",
			"--entrypoint", "/app/app.py",
			"-t", tag,
		})
		cmd.SetOut(os.Stderr)
		return cmd.Execute()
	}

	if err := run(); err != nil {
		t.Fatalf("build command failed: %v", err)
	}

	// The pushed image is pullable and has the expected layer count
	// (2 wheels + 1 source).
	ref, _ := name.ParseReference(tag)
	img, err := remote.Image(ref, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatalf("pull pushed image: %v", err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Fatalf("got %d layers, want 3", len(layers))
	}
	pushedDigest, _ := img.Digest()

	// Running the command again is reproducible: same digest.
	if err := run(); err != nil {
		t.Fatalf("second build failed: %v", err)
	}
	img2, err := remote.Image(ref, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := img2.Digest()
	if d2 != pushedDigest {
		t.Fatalf("re-running build produced a different digest: %s != %s", d2, pushedDigest)
	}
}
