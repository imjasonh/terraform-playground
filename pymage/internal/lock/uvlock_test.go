package lock

import (
	"os"
	"path/filepath"
	"testing"
)

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

[[package]]
name = "alpha"
version = "1.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://files.pythonhosted.org/packages/a/alpha-1.0.0-py3-none-any.whl", hash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
]

[[package]]
name = "native"
version = "2.0.0"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://example.com/native-2.0.0-py3-none-any.whl", hash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" },
    { url = "https://example.com/native-2.0.0-cp314-cp314-manylinux2014_x86_64.whl", hash = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" },
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
	if len(reqs) != 2 {
		t.Fatalf("got %d reqs, want 2 (virtual skipped): %+v", len(reqs), reqs)
	}
	if reqs[0].Name != "alpha" || len(reqs[0].Wheels) != 1 {
		t.Fatalf("alpha = %+v", reqs[0])
	}
	if reqs[0].Wheels[0].Filename != "alpha-1.0.0-py3-none-any.whl" {
		t.Errorf("filename = %q", reqs[0].Wheels[0].Filename)
	}
	if len(reqs[1].Wheels) != 2 {
		t.Fatalf("native wheels = %+v", reqs[1].Wheels)
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
	if len(reqs) != 2 {
		t.Fatalf("got %d", len(reqs))
	}
}
