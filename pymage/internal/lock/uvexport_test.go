package lock

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hermetic: exercise the export post-processing (marker filtering + artifact
// attach + extra neutralization) without invoking uv.
func TestMergeExportPins(t *testing.T) {
	// uv export-style output: universal pins with markers + hashes.
	export := strings.Join([]string{
		"core==1.0.0 \\",
		"    --hash=sha256:1111111111111111111111111111111111111111111111111111111111111111",
		"winonly==1.0.0 ; sys_platform == 'win32' \\",
		"    --hash=sha256:3333333333333333333333333333333333333333333333333333333333333333",
		"linonly==1.0.0 ; sys_platform == 'linux' \\",
		"    --hash=sha256:4444444444444444444444444444444444444444444444444444444444444444",
		"withextra==1.0.0 ; extra == 'gpu' and sys_platform == 'linux' \\",
		"    --hash=sha256:5555555555555555555555555555555555555555555555555555555555555555",
	}, "\n") + "\n"

	pins, err := Parse(strings.NewReader(export))
	if err != nil {
		t.Fatal(err)
	}

	lf := &uvLockFile{Package: []uvPackage{
		{Name: "core", Version: "1.0.0", Wheels: []uvArtifact{{URL: "https://e/core-1.0.0-py3-none-any.whl", Hash: "sha256:1111111111111111111111111111111111111111111111111111111111111111"}}},
		{Name: "linonly", Version: "1.0.0", Wheels: []uvArtifact{{URL: "https://e/linonly-1.0.0-py3-none-any.whl", Hash: "sha256:4444444444444444444444444444444444444444444444444444444444444444"}}},
		{Name: "withextra", Version: "1.0.0", Wheels: []uvArtifact{{URL: "https://e/withextra-1.0.0-py3-none-any.whl", Hash: "sha256:5555555555555555555555555555555555555555555555555555555555555555"}}},
	}}

	env := NewMarkerEnv("linux", "amd64", 3, 12)
	got, err := mergeExportPins(pins, lf, env)
	if err != nil {
		t.Fatal(err)
	}
	names := names(got)
	if !names["core"] || !names["linonly"] {
		t.Fatalf("missing expected runtime deps: %v", names)
	}
	if names["winonly"] {
		t.Error("winonly should be filtered out on linux (sys_platform marker)")
	}
	if !names["withextra"] {
		t.Error("withextra should be kept: extra clause is neutral, linux satisfies the rest")
	}
	// URLs attached from the lock.
	for _, r := range got {
		if NormalizeName(r.Name) == "core" && (len(r.Wheels) != 1 || r.Wheels[0].Filename != "core-1.0.0-py3-none-any.whl") {
			t.Errorf("core wheels not attached from lock: %+v", r.Wheels)
		}
	}
}

// integration: when uv is available, drive a real `uv lock` + Resolve via
// `uv export`. Skips without uv or network.
func TestResolveViaUVExport(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not installed")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pyproject.toml"), `[project]
name = "demo"
version = "0.1.0"
requires-python = ">=3.12,<3.13"
dependencies = ["idna==3.10"]

[dependency-groups]
dev = ["iniconfig==2.0.0"]

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"
`)
	writeFile(t, filepath.Join(dir, "demo", "__init__.py"), "")
	cmd := exec.Command("uv", "lock")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("uv lock failed (likely offline): %v\n%s", err, out)
	}

	lk, err := Load(filepath.Join(dir, "uv.lock"))
	if err != nil {
		t.Fatal(err)
	}
	reqs, err := lk.Resolve(Options{OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12})
	if err != nil {
		t.Fatal(err)
	}
	got := names(reqs)
	if !got["idna"] {
		t.Errorf("runtime dep idna missing: %v", got)
	}
	if got["iniconfig"] {
		t.Error("dev-group dep iniconfig should be excluded by --no-dev")
	}
	// uv.lock wheel URL should be attached for the downloader.
	for _, r := range reqs {
		if NormalizeName(r.Name) == "idna" && len(r.Wheels) == 0 {
			t.Error("idna wheel URL not attached from uv.lock")
		}
	}
}

func names(reqs []Requirement) map[string]bool {
	m := map[string]bool{}
	for _, r := range reqs {
		m[NormalizeName(r.Name)] = true
	}
	return m
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
