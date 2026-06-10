package wheel

import (
	"strings"
	"testing"

	"github.com/imjasonh/terraform-playground/pymage/internal/ptar"
	"github.com/imjasonh/terraform-playground/pymage/internal/testwheel"
)

var layout = Layout{Prefix: "/app/.venv", PythonTag: "python3.12"}

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path, _ := testwheel.Write(t, dir, testwheel.Spec{
		Name:    "examplepkg",
		Version: "1.2.3",
		Modules: map[string]string{
			"examplepkg/__init__.py": "VERSION = '1.2.3'\n",
			"examplepkg/cli.py":      "def main():\n    print('hi')\n",
		},
		ConsoleScripts: map[string]string{"example": "examplepkg.cli:main"},
	})
	return path
}

func TestParseFilename(t *testing.T) {
	name, version, err := ParseFilename("examplepkg-1.2.3-py3-none-any.whl")
	if err != nil {
		t.Fatal(err)
	}
	if name != "examplepkg" || version != "1.2.3" {
		t.Fatalf("got %q %q", name, version)
	}
	if _, _, err := ParseFilename("not-a-wheel.txt"); err == nil {
		t.Error("expected error for malformed filename")
	}
}

func TestFilesLayout(t *testing.T) {
	w, err := Open(writeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if w.Name != "examplepkg" || w.Version != "1.2.3" {
		t.Fatalf("parsed name/version = %q %q", w.Name, w.Version)
	}

	files, err := w.Files(layout)
	if err != nil {
		t.Fatal(err)
	}

	byPath := map[string]ptar.File{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	site := "app/.venv/lib/python3.12/site-packages"
	for _, want := range []string{
		site + "/examplepkg/__init__.py",
		site + "/examplepkg/cli.py",
		site + "/examplepkg-1.2.3.dist-info/METADATA",
	} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("missing installed file %q", want)
		}
	}

	script, ok := byPath["app/.venv/bin/example"]
	if !ok {
		t.Fatal("console script not generated")
	}
	if !script.Executable {
		t.Error("console script should be executable")
	}
	if !strings.Contains(string(script.Data), "from examplepkg.cli import main") {
		t.Errorf("script body unexpected:\n%s", script.Data)
	}
	if !strings.HasPrefix(string(script.Data), "#!") {
		t.Error("script should start with a shebang")
	}
}

func TestFilesDeterministic(t *testing.T) {
	path := writeFixture(t)
	digest := func() string {
		w, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = w.Close() }()
		files, err := w.Files(layout)
		if err != nil {
			t.Fatal(err)
		}
		l, err := ptar.Layer(files)
		if err != nil {
			t.Fatal(err)
		}
		d, err := l.Digest()
		if err != nil {
			t.Fatal(err)
		}
		return d.String()
	}
	d1, d2 := digest(), digest()
	if d1 != d2 {
		t.Fatalf("wheel layer digest is not deterministic: %s != %s", d1, d2)
	}
}
