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
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/imjasonh/terraform-playground/pymage/internal/testwheel"
)

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
