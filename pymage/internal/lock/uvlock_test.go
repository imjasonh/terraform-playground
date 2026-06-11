package lock

import (
	"os"
	"path/filepath"
	"testing"
)

// uvLockFixture exercises: a virtual + an editable local package (both
// skipped), the runtime closure of the editable project (alpha, its transitive
// beta, and extradep pulled by the requested alpha[x] extra), a multi-wheel
// package (native), and a dev-only package (linter) that must be excluded.
const uvLockFixture = `version = 1
requires-python = ">=3.14"

[[package]]
name = "virtual-pkg"
version = "0.1.0"
source = { virtual = "." }

[[package]]
name = "app"
version = "0.1.0"
source = { editable = "." }
dependencies = [
    { name = "alpha", extra = ["x"] },
    { name = "native" },
]

[package.dev-dependencies]
dev = [
    { name = "linter" },
]

[[package]]
name = "alpha"
version = "1.0.0"
source = { registry = "https://pypi.org/simple" }
dependencies = [
    { name = "beta" },
]
wheels = [
    { url = "https://files.pythonhosted.org/packages/a/alpha-1.0.0-py3-none-any.whl", hash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
]

[package.optional-dependencies]
x = [
    { name = "extradep" },
]

[[package]]
name = "beta"
version = "1.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://example.com/beta-1.0.0-py3-none-any.whl", hash = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" },
]

[[package]]
name = "extradep"
version = "1.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://example.com/extradep-1.0.0-py3-none-any.whl", hash = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" },
]

[[package]]
name = "native"
version = "2.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://example.com/native-2.0.0-py3-none-any.whl", hash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" },
    { url = "https://example.com/native-2.0.0-cp314-cp314-manylinux2014_x86_64.whl", hash = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" },
]

[[package]]
name = "linter"
version = "9.9.9"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://example.com/linter-9.9.9-py3-none-any.whl", hash = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" },
]
`

func TestParseUVLockFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uv.lock")
	if err := os.WriteFile(path, []byte(uvLockFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	reqs, err := ParseUVLockFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Requirement{}
	for _, r := range reqs {
		got[r.Name] = r
	}
	// Runtime closure: alpha, its transitive beta, extradep (alpha[x]), native.
	for _, want := range []string{"alpha", "beta", "extradep", "native"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing runtime dep %q (got %v)", want, keys(got))
		}
	}
	// Local packages skipped; dev-only package excluded.
	for _, bad := range []string{"app", "virtual-pkg", "linter"} {
		if _, ok := got[bad]; ok {
			t.Errorf("%q should not be installed", bad)
		}
	}
	if len(reqs) != 4 {
		t.Fatalf("got %d reqs, want 4: %v", len(reqs), keys(got))
	}
	if len(got["native"].Wheels) != 2 {
		t.Errorf("native should keep both wheels, got %d", len(got["native"].Wheels))
	}
	if got["alpha"].Wheels[0].Filename != "alpha-1.0.0-py3-none-any.whl" {
		t.Errorf("alpha filename = %q", got["alpha"].Wheels[0].Filename)
	}
}

func TestParseAnyPrefersUVLock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "uv.lock"), []byte(uvLockFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	reqs, err := ParseAny(filepath.Join(dir, "uv.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 4 {
		t.Fatalf("got %d, want 4", len(reqs))
	}
}

func names(reqs []Requirement) map[string]bool {
	m := map[string]bool{}
	for _, r := range reqs {
		m[NormalizeName(r.Name)] = true
	}
	return m
}

// closureLock exercises extras, workspace --package selection, and markers.
const closureLock = `version = 1

[[package]]
name = "svc"
version = "0.1.0"
source = { editable = "members/svc" }
dependencies = [
    { name = "core" },
    { name = "winonly", marker = "sys_platform == 'win32'" },
]

[package.optional-dependencies]
gpu = [
    { name = "accel" },
]

[[package]]
name = "worker"
version = "0.1.0"
source = { editable = "members/worker" }
dependencies = [
    { name = "worktool" },
]

[[package]]
name = "core"
version = "1.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [ { url = "https://e/core-1.0.0-py3-none-any.whl", hash = "sha256:1111111111111111111111111111111111111111111111111111111111111111" } ]

[[package]]
name = "accel"
version = "1.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [ { url = "https://e/accel-1.0.0-py3-none-any.whl", hash = "sha256:2222222222222222222222222222222222222222222222222222222222222222" } ]

[[package]]
name = "winonly"
version = "1.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [ { url = "https://e/winonly-1.0.0-py3-none-any.whl", hash = "sha256:3333333333333333333333333333333333333333333333333333333333333333" } ]

[[package]]
name = "worktool"
version = "1.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [ { url = "https://e/worktool-1.0.0-py3-none-any.whl", hash = "sha256:4444444444444444444444444444444444444444444444444444444444444444" } ]
`

func loadClosure(t *testing.T) *Lock {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "uv.lock")
	if err := os.WriteFile(p, []byte(closureLock), 0o644); err != nil {
		t.Fatal(err)
	}
	lk, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return lk
}

func TestUVClosureMarkers(t *testing.T) {
	lk := loadClosure(t)
	// Linux target: winonly (sys_platform == win32) is excluded.
	reqs, err := lk.Resolve(Options{OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12})
	if err != nil {
		t.Fatal(err)
	}
	got := names(reqs)
	if !got["core"] || !got["worktool"] {
		t.Fatalf("missing runtime deps: %v", got)
	}
	if got["winonly"] {
		t.Error("winonly should be excluded on linux (marker)")
	}
	if got["accel"] {
		t.Error("accel should be excluded without --extra gpu")
	}
}

func TestUVClosureExtras(t *testing.T) {
	lk := loadClosure(t)
	reqs, err := lk.Resolve(Options{Extras: []string{"gpu"}, OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !names(reqs)["accel"] {
		t.Errorf("--extra gpu should include accel: %v", names(reqs))
	}
}

func TestUVClosurePackage(t *testing.T) {
	lk := loadClosure(t)
	// --package svc: only svc's closure (core), not worker's worktool.
	reqs, err := lk.Resolve(Options{Package: "svc", OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12})
	if err != nil {
		t.Fatal(err)
	}
	got := names(reqs)
	if !got["core"] || got["worktool"] {
		t.Errorf("--package svc closure = %v, want {core} (no worktool)", got)
	}

	// --package worker: only worktool.
	reqs, err = lk.Resolve(Options{Package: "worker", OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12})
	if err != nil {
		t.Fatal(err)
	}
	got = names(reqs)
	if !got["worktool"] || got["core"] {
		t.Errorf("--package worker closure = %v, want {worktool}", got)
	}

	if _, err := lk.Resolve(Options{Package: "nope"}); err == nil {
		t.Error("expected error for unknown --package")
	}
}

func keys(m map[string]Requirement) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
