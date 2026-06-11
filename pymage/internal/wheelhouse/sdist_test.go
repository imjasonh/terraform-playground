package wheelhouse

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
)

func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelectBuiltWheelPurePython(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "timeout_decorator-0.5.0-py3-none-any.whl", "notes.txt")
	req := lock.Requirement{Name: "timeout-decorator", Version: "0.5.0"}
	target := wheel.Target{OS: "linux", Arch: "arm64", PyMajor: 3, PyMinor: 12}

	got, err := selectBuiltWheel(dir, req, target)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "timeout_decorator-0.5.0-py3-none-any.whl" {
		t.Fatalf("got %q", got)
	}
}

func TestSelectBuiltWheelIncompatibleCompiled(t *testing.T) {
	dir := t.TempDir()
	// A compiled wheel built for the host (linux x86_64) when targeting arm64.
	writeFiles(t, dir, "ujson-5.0.0-cp312-cp312-manylinux_2_17_x86_64.whl")
	req := lock.Requirement{Name: "ujson", Version: "5.0.0"}
	target := wheel.Target{OS: "linux", Arch: "arm64", PyMajor: 3, PyMinor: 12}

	if _, err := selectBuiltWheel(dir, req, target); err == nil {
		t.Fatal("expected incompatibility error for cross-arch compiled wheel")
	}
}

func TestSelectBuiltWheelWrongVersion(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "timeout_decorator-0.4.0-py3-none-any.whl")
	req := lock.Requirement{Name: "timeout-decorator", Version: "0.5.0"}
	target := wheel.Target{OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12}

	if _, err := selectBuiltWheel(dir, req, target); err == nil {
		t.Fatal("expected error: built wheel version mismatch")
	}
}

func TestBuiltPathKey(t *testing.T) {
	c := &wheelCache{dir: "/tmp/c"}
	target := wheel.Target{OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12}
	key := "abc123" + "-" + targetKey(target)
	want := filepath.Join("/tmp/c", "built", key+".whl")
	if got := c.builtPath(key); got != want {
		t.Fatalf("builtPath = %q, want %q", got, want)
	}
}
