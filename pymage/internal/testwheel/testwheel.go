// Package testwheel synthesizes minimal but valid Python wheels on disk for use
// in tests, so tests stay hermetic (no pip, no network).
package testwheel

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Spec describes a wheel to synthesize.
type Spec struct {
	// Name is the distribution name (use underscores, no dashes).
	Name string
	// Version is the version (no dashes).
	Version string
	// Modules maps top-level module file paths (e.g. "foo/__init__.py") to
	// their contents, placed in the wheel root (i.e. into site-packages).
	Modules map[string]string
	// ConsoleScripts maps script name -> "module:attr" entry points.
	ConsoleScripts map[string]string
}

// Write creates the wheel file in dir and returns its path and hex sha256.
func Write(t *testing.T, dir string, s Spec) (path, sha string) {
	t.Helper()
	data := Bytes(t, s)
	name := fmt.Sprintf("%s-%s-py3-none-any.whl", s.Name, s.Version)
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write wheel: %v", err)
	}
	sum := sha256.Sum256(data)
	return path, hex.EncodeToString(sum[:])
}

// Bytes returns the wheel zip bytes for the spec, deterministically.
func Bytes(t *testing.T, s Spec) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	add := func(name, content string) {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     name,
			Method:   zip.Store, // stored: stable bytes
			Modified: time.Unix(0, 0).UTC(),
		})
		if err != nil {
			t.Fatalf("zip header %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}

	for p, c := range s.Modules {
		add(p, c)
	}

	distInfo := fmt.Sprintf("%s-%s.dist-info", s.Name, s.Version)
	add(distInfo+"/METADATA", fmt.Sprintf("Metadata-Version: 2.1\nName: %s\nVersion: %s\n", s.Name, s.Version))
	add(distInfo+"/WHEEL", "Wheel-Version: 1.0\nGenerator: testwheel\nRoot-Is-Purelib: true\nTag: py3-none-any\n")
	if len(s.ConsoleScripts) > 0 {
		ep := "[console_scripts]\n"
		for name, target := range s.ConsoleScripts {
			ep += fmt.Sprintf("%s = %s\n", name, target)
		}
		add(distInfo+"/entry_points.txt", ep)
	}
	add(distInfo+"/RECORD", "")

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}
