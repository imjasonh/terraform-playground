package wheel

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/imjasonh/terraform-playground/pymage/internal/ptar"
	"github.com/imjasonh/terraform-playground/pymage/internal/testwheel"
)

// Installer-added metadata that the oracle (uv/pip) writes but that isn't part
// of the wheel payload; excluded from the file-set comparison.
var installerExtras = map[string]bool{
	"RECORD": true, "INSTALLER": true, "REQUESTED": true,
	"direct_url.json": true, "uv_cache.json": true,
}

// TestConformanceWithPipInstall verifies that pymage places a wheel's files at
// the same site-packages-relative paths as a real installer (uv pip / pip),
// and that it generates the same console-script launchers.
//
// This is the key fidelity check: installing a *wheel* runs no project code
// (PEP 427 install = unpack + synthesize scripts/metadata), so we can use the
// real installer as an oracle without any RCE risk — unlike building an sdist.
// Skips when neither uv nor pip is available.
func TestConformanceWithPipInstall(t *testing.T) {
	installer := findInstaller(t)
	if installer == nil {
		t.Skip("neither uv nor pip available")
	}

	dir := t.TempDir()
	whlPath, _ := testwheel.Write(t, dir, testwheel.Spec{
		Name:    "demolib",
		Version: "1.2.3",
		Modules: map[string]string{
			"demolib/__init__.py":     "VERSION = '1.2.3'\n",
			"demolib/cli.py":          "def main():\n    print('hi')\n",
			"demolib/sub/helper.py":   "x = 1\n",
			"demolib/sub/__init__.py": "",
		},
		ConsoleScripts: map[string]string{"demo": "demolib.cli:main"},
	})

	// --- oracle: install the wheel with uv/pip into a target dir ---
	target := filepath.Join(dir, "oracle")
	if out, err := installer(whlPath, target); err != nil {
		t.Skipf("installer failed (treating as environment issue): %v\n%s", err, out)
	}
	oracleLib, oracleBin := collectOracle(t, target)

	// --- pymage: compute the install layout from the wheel ---
	w, err := Open(whlPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	files, err := w.Files(layout)
	if err != nil {
		t.Fatal(err)
	}
	pyLib, pyBin := collectPymage(files)

	// Package payload (non-dist-info) files must match exactly.
	oraclePayload := nonDistInfo(oracleLib)
	pyPayload := nonDistInfo(pyLib)
	if !equalSets(oraclePayload, pyPayload) {
		t.Errorf("payload file set differs:\n  pip-only:    %v\n  pymage-only: %v",
			diff(oraclePayload, pyPayload), diff(pyPayload, oraclePayload))
	}

	// pymage's dist-info files must be a subset of the oracle's (the oracle adds
	// RECORD/INSTALLER/etc. that we don't synthesize).
	for f := range distInfoOnly(pyLib) {
		if !oracleLib[f] {
			t.Errorf("pymage emitted dist-info file the installer did not: %q", f)
		}
	}

	// Both must generate the console-script launcher.
	if !pyBin["demo"] {
		t.Errorf("pymage did not generate console script 'demo'; bin=%v", keys(pyBin))
	}
	if !oracleBin["demo"] {
		t.Logf("note: oracle bin did not contain 'demo' (bin=%v) — installer may place scripts elsewhere", keys(oracleBin))
	}
}

func findInstaller(t *testing.T) func(whl, target string) ([]byte, error) {
	t.Helper()
	if _, err := exec.LookPath("uv"); err == nil {
		return func(whl, target string) ([]byte, error) {
			cmd := exec.Command("uv", "pip", "install", "--no-deps", "--no-compile", "--target", target, whl)
			return cmd.CombinedOutput()
		}
	}
	if _, err := exec.LookPath("pip"); err == nil {
		return func(whl, target string) ([]byte, error) {
			cmd := exec.Command("pip", "install", "--no-deps", "--no-compile", "--no-index", "--target", target, whl)
			return cmd.CombinedOutput()
		}
	}
	return nil
}

// collectOracle returns the set of site-packages-relative paths under target
// (excluding bin and .pyc) and the set of script basenames in target/bin.
func collectOracle(t *testing.T, target string) (lib, bin map[string]bool) {
	t.Helper()
	lib, bin = map[string]bool{}, map[string]bool{}
	_ = filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(target, p)
		rel = filepath.ToSlash(rel)
		switch {
		case rel == ".lock" || strings.HasSuffix(rel, ".pyc") || strings.Contains(rel, "__pycache__"):
			return nil
		case strings.HasPrefix(rel, "bin/"):
			bin[strings.TrimPrefix(rel, "bin/")] = true
		default:
			if base := filepath.Base(rel); installerExtras[base] {
				return nil
			}
			lib[rel] = true
		}
		return nil
	})
	return lib, bin
}

// collectPymage maps pymage's ptar paths to site-packages-relative lib paths
// and bin script basenames.
func collectPymage(files []ptar.File) (lib, bin map[string]bool) {
	lib, bin = map[string]bool{}, map[string]bool{}
	site := layout.SitePackages() + "/"
	bdir := layout.BinDir() + "/"
	for _, f := range files {
		p := f.Path
		switch {
		case strings.HasPrefix(p, site):
			rel := strings.TrimPrefix(p, site)
			if base := filepath.Base(rel); installerExtras[base] {
				continue
			}
			lib[rel] = true
		case strings.HasPrefix(p, bdir):
			bin[strings.TrimPrefix(p, bdir)] = true
		}
	}
	return lib, bin
}

func nonDistInfo(m map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		if !strings.Contains(k, ".dist-info/") {
			out[k] = true
		}
	}
	return out
}

func distInfoOnly(m map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		if strings.Contains(k, ".dist-info/") {
			out[k] = true
		}
	}
	return out
}

func equalSets(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func diff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
