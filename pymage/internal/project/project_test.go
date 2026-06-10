package project

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiscoverExample(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "example")
	info, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(info.LockFile) != "uv.lock" {
		t.Fatalf("lock = %q", info.LockFile)
	}
	if len(info.Entrypoint) != 1 || info.Entrypoint[0] != "example" {
		t.Fatalf("entrypoint = %v", info.Entrypoint)
	}
	found := false
	for _, e := range info.ExtraEnv {
		if e == "PYTHONPATH=/app/src" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected PYTHONPATH env, got %v", info.ExtraEnv)
	}

	if info.Config.Base != "cgr.dev/chainguard/python:latest" {
		t.Errorf("config base = %q", info.Config.Base)
	}
	if got := info.Config.Platforms; len(got) != 2 || got[0] != "linux/amd64" || got[1] != "linux/arm64" {
		t.Errorf("config platforms = %v", got)
	}
	if info.Config.LayerStrategy != "per-wheel" {
		t.Errorf("config layer-strategy = %q", info.Config.LayerStrategy)
	}
}

func TestConfigFindLinksRelativeToRoot(t *testing.T) {
	dir := t.TempDir()
	pyproject := `[project]
name = "demo"
version = "0.1.0"

[tool.pymage]
find-links = ["wheelhouse", "/abs/wheels"]
`
	mustWrite(t, filepath.Join(dir, "pyproject.toml"), pyproject)
	mustWrite(t, filepath.Join(dir, "requirements.txt"), "alpha==1.0\n")

	info, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "wheelhouse")
	if got := info.Config.FindLinks; len(got) != 2 || got[0] != want || got[1] != "/abs/wheels" {
		t.Fatalf("find-links = %v, want [%s /abs/wheels]", got, want)
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
