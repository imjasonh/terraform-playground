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

func keys(m map[string]Requirement) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
