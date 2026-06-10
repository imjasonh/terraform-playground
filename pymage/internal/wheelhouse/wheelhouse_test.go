package wheelhouse

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/testwheel"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
)

var linuxAmd64Py312 = wheel.Target{OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12}

func TestResolveSortedAndVerified(t *testing.T) {
	dir := t.TempDir()
	_, shaB := testwheel.Write(t, dir, testwheel.Spec{Name: "beta", Version: "2.0", Modules: map[string]string{"beta.py": "x=1\n"}})
	_, shaA := testwheel.Write(t, dir, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha.py": "x=1\n"}})

	reqs := []lock.Requirement{
		{Name: "Beta", Version: "2.0", Hashes: []string{shaB}},
		{Name: "alpha", Version: "1.0", Hashes: []string{shaA}},
	}
	got, err := Resolve(reqs, []string{dir}, linuxAmd64Py312)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d wheels", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "Beta" {
		t.Fatalf("not sorted by normalized name: %+v", got)
	}
	if got[0].SHA256 != shaA {
		t.Errorf("alpha sha mismatch")
	}
}

func TestResolveHashMismatch(t *testing.T) {
	dir := t.TempDir()
	testwheel.Write(t, dir, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha.py": "x=1\n"}})
	reqs := []lock.Requirement{{Name: "alpha", Version: "1.0", Hashes: []string{"deadbeef"}}}
	if _, err := Resolve(reqs, []string{dir}, linuxAmd64Py312); err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

func TestResolveMissing(t *testing.T) {
	dir := t.TempDir()
	reqs := []lock.Requirement{{Name: "ghost", Version: "9.9"}}
	if _, err := Resolve(reqs, []string{dir}, linuxAmd64Py312); err == nil {
		t.Fatal("expected missing-wheel error")
	}
}

// TestResolveIncompatiblePlatform proves that a wheel for the wrong platform is
// rejected rather than silently installed.
func TestResolveIncompatiblePlatform(t *testing.T) {
	dir := t.TempDir()
	// A wheel that only has an x86_64 platform tag.
	writeNamed(t, dir, "native-1.0-cp312-cp312-manylinux2014_x86_64.whl")
	reqs := []lock.Requirement{{Name: "native", Version: "1.0"}}

	if _, err := Resolve(reqs, []string{dir}, wheel.Target{OS: "linux", Arch: "arm64", PyMajor: 3, PyMinor: 12}); err == nil {
		t.Fatal("expected incompatible-platform error for arm64 target")
	}
	if _, err := Resolve(reqs, []string{dir}, linuxAmd64Py312); err != nil {
		t.Fatalf("x86_64 wheel should resolve for amd64 target: %v", err)
	}
}

// TestResolvePrefersPlatformSpecific picks the platform-specific wheel when both
// a pure-python and a native wheel exist for the same version.
func TestResolvePrefersPlatformSpecific(t *testing.T) {
	dir := t.TempDir()
	writeNamed(t, dir, "pkg-1.0-py3-none-any.whl")
	nativePath := writeNamed(t, dir, "pkg-1.0-cp312-cp312-manylinux2014_x86_64.whl")
	reqs := []lock.Requirement{{Name: "pkg", Version: "1.0"}}

	got, err := Resolve(reqs, []string{dir}, linuxAmd64Py312)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != nativePath {
		t.Fatalf("expected the native wheel to be preferred, got %s", got[0].Path)
	}
}

// TestResolvePrefersHigherBuildTag picks the highest build tag when two wheels
// differ only by build.
func TestResolvePrefersHigherBuildTag(t *testing.T) {
	dir := t.TempDir()
	writeNamed(t, dir, "pkg-1.0-1-py3-none-any.whl")
	high := writeNamed(t, dir, "pkg-1.0-2-py3-none-any.whl")
	reqs := []lock.Requirement{{Name: "pkg", Version: "1.0"}}

	got, err := Resolve(reqs, []string{dir}, linuxAmd64Py312)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != high {
		t.Fatalf("expected the higher build tag to be preferred, got %s", got[0].Path)
	}
}

// writeNamed writes a minimal (not necessarily valid-zip) file with an exact
// wheel filename, used only to exercise filename-based tag resolution.
func writeNamed(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
