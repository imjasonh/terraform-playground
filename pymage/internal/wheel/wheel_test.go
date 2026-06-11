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

func TestParseTagsWithBuildTag(t *testing.T) {
	tg, err := ParseTags("foo-1.0-1-cp312-cp312-manylinux2014_x86_64.whl")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Name != "foo" || tg.Version != "1.0" {
		t.Fatalf("name/version = %q %q", tg.Name, tg.Version)
	}
	if tg.Build != "1" || tg.BuildRank() != 1 {
		t.Fatalf("build = %q rank=%d", tg.Build, tg.BuildRank())
	}
	if tg.Python != "cp312" || tg.ABI != "cp312" || tg.Platform != "manylinux2014_x86_64" {
		t.Fatalf("tags = %+v", tg)
	}

	noBuild, err := ParseTags("foo-1.0-py3-none-any.whl")
	if err != nil {
		t.Fatal(err)
	}
	if noBuild.Build != "" || noBuild.BuildRank() != 0 {
		t.Fatalf("expected no build tag, got %q", noBuild.Build)
	}
}

func TestCompatibleWith(t *testing.T) {
	t312 := Target{OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12}
	arm := Target{OS: "linux", Arch: "arm64", PyMajor: 3, PyMinor: 12}
	cases := []struct {
		py, abi, plat string
		target        Target
		want          bool
	}{
		{"py3", "none", "any", t312, true},     // pure python
		{"py2.py3", "none", "any", t312, true}, // compressed set
		{"cp312", "cp312", "manylinux2014_x86_64", t312, true},
		{"cp312", "cp312", "manylinux2014_x86_64", arm, false},  // wrong arch
		{"cp311", "cp311", "manylinux2014_x86_64", t312, false}, // wrong py
		{"cp37", "abi3", "manylinux2014_x86_64", t312, true},    // stable abi, older ok
		{"cp313", "abi3", "manylinux2014_x86_64", t312, false},  // stable abi, newer not ok
		{"cp312", "cp312", "macosx_11_0_arm64", t312, false},    // wrong platform
	}
	for _, c := range cases {
		tg := Tags{Python: c.py, ABI: c.abi, Platform: c.plat}
		if got := tg.CompatibleWith(c.target); got != c.want {
			t.Errorf("Tags{%s-%s-%s}.CompatibleWith(%+v) = %v, want %v", c.py, c.abi, c.plat, c.target, got, c.want)
		}
	}
}

func TestFilesRejectsEscapingMember(t *testing.T) {
	dir := t.TempDir()
	path, _ := testwheel.Write(t, dir, testwheel.Spec{
		Name:    "evil",
		Version: "1.0",
		Modules: map[string]string{"../../../../../../etc/passwd": "x\n"},
	})
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Files(layout); err == nil {
		t.Fatal("expected error for member escaping the install prefix")
	}
}

func TestFilesRejectsInternalDotDot(t *testing.T) {
	// A member with a ".." segment that resolves back under the prefix (here,
	// out of site-packages into the prefix's bin) must still be rejected.
	dir := t.TempDir()
	path, _ := testwheel.Write(t, dir, testwheel.Spec{
		Name:    "evil",
		Version: "1.0",
		Modules: map[string]string{"pkg/../../../../bin/evil": "x\n"},
	})
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Files(layout); err == nil {
		t.Fatal("expected error for member with an internal '..' segment")
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

func TestConsoleScriptExtrasStripped(t *testing.T) {
	dir := t.TempDir()
	path, _ := testwheel.Write(t, dir, testwheel.Spec{
		Name:    "tool",
		Version: "1.0",
		Modules: map[string]string{"tool/cli.py": "def main():\n    pass\n"},
		ConsoleScripts: map[string]string{
			"tool":    "tool.cli:main [fast]", // extras must be stripped
			"broken":  ":main",                // no module: must be skipped
			"colored": "tool.cli:main[color]", // extras with no space
		},
	})
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	files, err := w.Files(layout)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = string(f.Data)
	}

	bin := "app/.venv/bin/"
	for _, name := range []string{"tool", "colored"} {
		body, ok := byPath[bin+name]
		if !ok {
			t.Fatalf("missing console script %q", name)
		}
		if strings.Contains(body, "[") {
			t.Errorf("script %q still contains extras:\n%s", name, body)
		}
		if !strings.Contains(body, "from tool.cli import main") || !strings.Contains(body, "sys.exit(main())") {
			t.Errorf("script %q has unexpected body:\n%s", name, body)
		}
	}
	if _, ok := byPath[bin+"broken"]; ok {
		t.Error("module-less entry point should not produce a launcher")
	}
}

func TestParseConsoleScriptsLongLineNotTruncated(t *testing.T) {
	// A comment line longer than bufio.Scanner's default 64 KiB token limit
	// would have made a Scanner stop early and silently drop later entries.
	longComment := "# " + strings.Repeat("x", 200*1024)
	content := longComment + "\n[console_scripts]\ntool = pkg.mod:main\n"
	eps := parseConsoleScripts(content)
	if ep, ok := eps["tool"]; !ok || ep.module != "pkg.mod" || ep.attr != "main" {
		t.Fatalf("entry after a very long line was dropped: %+v", eps)
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
