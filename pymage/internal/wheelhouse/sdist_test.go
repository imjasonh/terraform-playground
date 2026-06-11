package wheelhouse

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
)

// tgz packs files (paths relative to a "<root>/" prefix) into a gzip tar, the
// shape of a real sdist.
func tgz(t *testing.T, root string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		data := []byte(files[n])
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + n, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// wheelEntries returns the set of entry names in a built wheel zip.
func wheelEntries(t *testing.T, b []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, f := range zr.File {
		out[f.Name] = true
	}
	return out
}

// setup.py + egg-info shape (e.g. timeout-decorator): repack from top_level.txt.
func TestRepackEggInfoLayout(t *testing.T) {
	pkginfo := "Metadata-Version: 2.1\nName: demo\nVersion: 1.0.0\nSummary: x\n\nbody text\n"
	gz := tgz(t, "demo-1.0.0", map[string]string{
		"PKG-INFO":                    pkginfo,
		"setup.py":                    "from setuptools import setup\nsetup()\n",
		"MANIFEST.in":                 "include README.rst\n", // must NOT cause rejection
		"demo/__init__.py":            "VALUE = 42\n",
		"demo/core.py":                "def f():\n    return 1\n",
		"demo.egg-info/PKG-INFO":      pkginfo,
		"demo.egg-info/top_level.txt": "demo\n",
		"demo.egg-info/SOURCES.txt":   "demo/__init__.py\n",
	})
	name, wb, err := synthWheelFromSdist(gz, lock.Requirement{Name: "demo", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "demo-1.0.0-py3-none-any.whl" {
		t.Errorf("wheel name = %q", name)
	}
	got := wheelEntries(t, wb)
	for _, want := range []string{"demo/__init__.py", "demo/core.py", "demo-1.0.0.dist-info/METADATA", "demo-1.0.0.dist-info/WHEEL", "demo-1.0.0.dist-info/RECORD"} {
		if !got[want] {
			t.Errorf("wheel missing %q (have %v)", want, keysOf(got))
		}
	}
	// egg-info/SOURCES.txt and setup.py must not be in the wheel.
	for _, bad := range []string{"setup.py", "demo.egg-info/top_level.txt", "MANIFEST.in"} {
		if got[bad] {
			t.Errorf("wheel should not contain %q", bad)
		}
	}
	// The repacked wheel must be openable by our own installer.
	mustOpenWheel(t, name, wb)
}

// src-layout declarative (e.g. iniconfig): repack from src/ + pyproject scripts.
func TestRepackSrcLayoutDeclarative(t *testing.T) {
	pkginfo := "Metadata-Version: 2.2\nName: ini\nVersion: 2.0.0\n"
	gz := tgz(t, "ini-2.0.0", map[string]string{
		"PKG-INFO": pkginfo,
		"pyproject.toml": `[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"
[project]
name = "ini"
version = "2.0.0"
[project.scripts]
ini-cli = "ini:main"
`,
		"src/ini/__init__.py": "def main():\n    return 0\n",
		"src/ini/_parse.py":   "X = 1\n",
		"src/ini/py.typed":    "",
	})
	name, wb, err := synthWheelFromSdist(gz, lock.Requirement{Name: "ini", Version: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	got := wheelEntries(t, wb)
	for _, want := range []string{"ini/__init__.py", "ini/_parse.py", "ini/py.typed", "ini-2.0.0.dist-info/entry_points.txt"} {
		if !got[want] {
			t.Errorf("wheel missing %q (have %v)", want, keysOf(got))
		}
	}
	mustOpenWheel(t, name, wb)
}

func TestRepackRejections(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "native sources",
			files: map[string]string{
				"PKG-INFO":            "Metadata-Version: 2.1\nName: nat\nVersion: 1.0.0\n",
				"pyproject.toml":      "[project]\nname=\"nat\"\nversion=\"1.0.0\"\n",
				"src/nat/__init__.py": "x=1\n",
				"src/nat/_ext.c":      "int main(){return 0;}\n",
			},
			want: "native",
		},
		{
			name: "compiling backend",
			files: map[string]string{
				"PKG-INFO":           "Metadata-Version: 2.1\nName: cy\nVersion: 1.0.0\n",
				"pyproject.toml":     "[build-system]\nrequires=[\"Cython\",\"setuptools\"]\n[project]\nname=\"cy\"\nversion=\"1.0.0\"\n",
				"src/cy/__init__.py": "x=1\n",
			},
			want: "compiling backend",
		},
		{
			name: "data_files",
			files: map[string]string{
				"PKG-INFO":       "Metadata-Version: 2.1\nName: df\nVersion: 1.0.0\n",
				"setup.py":       "setup(data_files=[('etc', ['x.conf'])])\n",
				"df/__init__.py": "x=1\n",
			},
			want: "data_files",
		},
		{
			name: "version mismatch",
			files: map[string]string{
				"PKG-INFO":       "Metadata-Version: 2.1\nName: vm\nVersion: 9.9.9\n",
				"vm/__init__.py": "x=1\n",
			},
			want: "Version",
		},
		{
			name: "no PKG-INFO",
			files: map[string]string{
				"nope/__init__.py": "x=1\n",
			},
			want: "PKG-INFO",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := "pkg-1.0.0"
			gz := tgz(t, root, c.files)
			req := lock.Requirement{Name: pkgName(c.files), Version: "1.0.0"}
			_, _, err := synthWheelFromSdist(gz, req)
			if err == nil {
				t.Fatalf("expected rejection mentioning %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q should mention %q", err.Error(), c.want)
			}
		})
	}
}

// Without --repack-sdists, an sdist-only requirement errors with guidance.
func TestSdistOnlyRequiresOptIn(t *testing.T) {
	req := lock.Requirement{
		Name: "timeout-decorator", Version: "0.5.0",
		Sdist: &lock.SdistRef{URL: "https://e/timeout_decorator-0.5.0.tar.gz", SHA256: "deadbeef", Filename: "timeout_decorator-0.5.0.tar.gz"},
	}
	target := wheel.Target{OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12}
	_, err := ResolveContext(context.Background(), []lock.Requirement{req}, nil, target, t.TempDir(), false)
	if err == nil {
		t.Fatal("expected an error without --repack-sdists")
	}
	for _, want := range []string{"source distribution", "--repack-sdists", "--find-links"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

func mustOpenWheel(t *testing.T, name string, wb []byte) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, wb, 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := wheel.Open(p)
	if err != nil {
		t.Fatalf("repacked wheel not openable: %v", err)
	}
	_ = w.Close()
}

func pkgName(files map[string]string) string {
	for n := range files {
		if n == "PKG-INFO" {
			for _, ln := range strings.Split(files[n], "\n") {
				if strings.HasPrefix(ln, "Name:") {
					return strings.TrimSpace(strings.TrimPrefix(ln, "Name:"))
				}
			}
		}
	}
	return "pkg"
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
