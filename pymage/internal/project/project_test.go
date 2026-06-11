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
	// The [project.scripts] "example = example:main" must become a direct
	// python invocation (not a bare "example" launcher, which pymage never
	// installs).
	wantEP := []string{"python", "-c", "import sys; from example import main; sys.exit(main())"}
	if len(info.Entrypoint) != len(wantEP) {
		t.Fatalf("entrypoint = %v, want %v", info.Entrypoint, wantEP)
	}
	for i := range wantEP {
		if info.Entrypoint[i] != wantEP[i] {
			t.Fatalf("entrypoint = %v, want %v", info.Entrypoint, wantEP)
		}
	}
	if !info.SrcLayout {
		t.Fatal("expected SrcLayout=true for the src/-layout example project")
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
	absWheels := t.TempDir() // absolute on every OS (incl. Windows drive letters)
	// TOML literal strings (single quotes) so Windows backslashes aren't treated
	// as escapes.
	pyproject := "[project]\nname = \"demo\"\nversion = \"0.1.0\"\n\n" +
		"[tool.pymage]\nfind-links = ['wheelhouse', '" + absWheels + "']\n"
	mustWrite(t, filepath.Join(dir, "pyproject.toml"), pyproject)
	mustWrite(t, filepath.Join(dir, "requirements.txt"), "alpha==1.0\n")

	info, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "wheelhouse")
	if got := info.Config.FindLinks; len(got) != 2 || got[0] != want || got[1] != absWheels {
		t.Fatalf("find-links = %v, want [%s %s]", got, want, absWheels)
	}
}

func TestScriptEntrypoint(t *testing.T) {
	cases := []struct {
		target string
		want   []string
	}{
		{"pkg.mod:main", []string{"python", "-c", "import sys; from pkg.mod import main; sys.exit(main())"}},
		{"pkg:obj.method", []string{"python", "-c", "import sys; from pkg import obj; sys.exit(obj.method())"}},
		{"pkg.cli", []string{"python", "-m", "pkg.cli"}}, // no attr -> run as module
		{":main", nil}, // no module -> unusable
	}
	for _, c := range cases {
		got := scriptEntrypoint(c.target)
		if len(got) != len(c.want) {
			t.Errorf("scriptEntrypoint(%q) = %v, want %v", c.target, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("scriptEntrypoint(%q) = %v, want %v", c.target, got, c.want)
				break
			}
		}
	}
}

func TestConfigLayerBudget(t *testing.T) {
	dir := t.TempDir()
	pyproject := "[project]\nname = \"demo\"\nversion = \"0.1.0\"\n\n" +
		"[tool.pymage]\nmax-layers = 50\nmax-wheel-layers = 8\nlayer-strategy = \"auto\"\n"
	mustWrite(t, filepath.Join(dir, "pyproject.toml"), pyproject)
	mustWrite(t, filepath.Join(dir, "requirements.txt"), "alpha==1.0\n")

	info, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.MaxLayers != 50 {
		t.Errorf("max-layers = %d, want 50", info.Config.MaxLayers)
	}
	if info.Config.MaxWheelLayers != 8 {
		t.Errorf("max-wheel-layers = %d, want 8", info.Config.MaxWheelLayers)
	}
	if info.Config.LayerStrategy != "auto" {
		t.Errorf("layer-strategy = %q, want auto", info.Config.LayerStrategy)
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
