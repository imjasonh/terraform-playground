package build

import (
	"archive/tar"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/imjasonh/terraform-playground/pymage/internal/cache"
	"github.com/imjasonh/terraform-playground/pymage/internal/ptar"
	"github.com/imjasonh/terraform-playground/pymage/internal/testwheel"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheelhouse"
)

func TestBasePlatforms(t *testing.T) {
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")
	ctx := context.Background()

	mk := func(os, arch string) v1.Image {
		img, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{OS: os, Architecture: arch})
		if err != nil {
			t.Fatal(err)
		}
		return img
	}

	// A multi-arch index with an attestation-style "unknown" entry that must be
	// ignored.
	idx := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: mk("linux", "amd64"), Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
		mutate.IndexAddendum{Add: mk("linux", "arm64"), Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
		mutate.IndexAddendum{Add: mk("unknown", "unknown"), Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"}}},
	)
	idxRef, _ := name.ParseReference(host + "/multi:latest")
	if err := remote.WriteIndex(idxRef, idx, remote.WithContext(ctx)); err != nil {
		t.Fatal(err)
	}

	plats, err := BasePlatforms(ctx, host+"/multi:latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range plats {
		got[p.OS+"/"+p.Architecture] = true
	}
	if len(plats) != 2 || !got["linux/amd64"] || !got["linux/arm64"] {
		t.Fatalf("index platforms = %v, want linux/amd64 + linux/arm64 (no unknown)", plats)
	}

	// A plain single-arch image returns its one platform.
	imgRef, _ := name.ParseReference(host + "/single:latest")
	if err := remote.Write(imgRef, mk("linux", "arm64"), remote.WithContext(ctx)); err != nil {
		t.Fatal(err)
	}
	plats, err = BasePlatforms(ctx, host+"/single:latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plats) != 1 || plats[0].OS != "linux" || plats[0].Architecture != "arm64" {
		t.Fatalf("single platforms = %v, want [linux/arm64]", plats)
	}
}

func ptarLayer(t *testing.T, name, content string) (v1.Layer, error) {
	t.Helper()
	return ptar.Layer([]ptar.File{{Path: name, Data: []byte(content)}})
}

func layerPaths(t *testing.T, l v1.Layer) map[string]bool {
	t.Helper()
	rc, err := l.Uncompressed()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	names := map[string]bool{}
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			names[h.Name] = true
		}
	}
	return names
}

var testLayout = wheel.Layout{Prefix: "/app/.venv", PythonTag: "python3.12"}

func mkWheel(t *testing.T, dir, name, version string) wheelhouse.ResolvedWheel {
	t.Helper()
	path, sha := testwheel.Write(t, dir, testwheel.Spec{
		Name:    name,
		Version: version,
		Modules: map[string]string{name + "/__init__.py": "V='" + version + "'\n"},
	})
	return wheelhouse.ResolvedWheel{Name: name, Version: version, Path: path, SHA256: sha}
}

func baseOpts(wheels []wheelhouse.ResolvedWheel) Options {
	return Options{
		Base:       empty.Image,
		Wheels:     wheels,
		Layout:     testLayout,
		Strategy:   PerWheel,
		WorkingDir: "/app",
		Entrypoint: []string{"python", "-m", "app"},
	}
}

func TestBuildReproducible(t *testing.T) {
	dir := t.TempDir()
	wheels := []wheelhouse.ResolvedWheel{mkWheel(t, dir, "alpha", "1.0"), mkWheel(t, dir, "beta", "2.0")}

	img1, err := Build(baseOpts(wheels))
	if err != nil {
		t.Fatal(err)
	}
	img2, err := Build(baseOpts(wheels))
	if err != nil {
		t.Fatal(err)
	}
	d1, err := img1.Digest()
	if err != nil {
		t.Fatal(err)
	}
	d2, err := img2.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("image not reproducible: %s vs %s", d1, d2)
	}
}

func TestConfigAndLayerCount(t *testing.T) {
	dir := t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wheels := []wheelhouse.ResolvedWheel{mkWheel(t, dir, "alpha", "1.0"), mkWheel(t, dir, "beta", "2.0")}
	opts := baseOpts(wheels)
	opts.SourceDir = src
	opts.User = "65532"
	opts.Labels = map[string]string{"org.test": "yes"}

	img, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}

	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 { // 2 wheels + 1 source
		t.Fatalf("got %d layers, want 3", len(layers))
	}

	cf, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	c := cf.Config
	env := strings.Join(c.Env, "\n")
	for _, want := range []string{
		"VIRTUAL_ENV=/app/.venv",
		"PYTHONPATH=/app/.venv/lib/python3.12/site-packages",
		"PATH=/app/.venv/bin:",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %q; got:\n%s", want, env)
		}
	}
	if c.WorkingDir != "/app" {
		t.Errorf("workdir = %q", c.WorkingDir)
	}
	if c.User != "65532" {
		t.Errorf("user = %q", c.User)
	}
	if strings.Join(c.Entrypoint, " ") != "python -m app" {
		t.Errorf("entrypoint = %v", c.Entrypoint)
	}
	if c.Labels["org.test"] != "yes" {
		t.Errorf("label not set: %v", c.Labels)
	}
	if !cf.Created.Equal(epoch()) {
		t.Errorf("config Created = %v, want epoch", cf.Created)
	}
}

// TestPerWheelOnlyNewDepChangesLayers is the central guarantee: adding a new
// dependency adds exactly one new layer and leaves every existing dependency
// layer byte-identical (same digest).
func TestPerWheelOnlyNewDepChangesLayers(t *testing.T) {
	dir := t.TempDir()
	a := mkWheel(t, dir, "alpha", "1.0")
	b := mkWheel(t, dir, "beta", "2.0")
	c := mkWheel(t, dir, "gamma", "3.0")

	digestsOf := func(wheels []wheelhouse.ResolvedWheel) []string {
		img, err := Build(baseOpts(wheels))
		if err != nil {
			t.Fatal(err)
		}
		layers, err := img.Layers()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range layers {
			d, err := l.Digest()
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, d.String())
		}
		return out
	}

	before := digestsOf([]wheelhouse.ResolvedWheel{a, b})
	after := digestsOf([]wheelhouse.ResolvedWheel{a, b, c})

	if len(before) != 2 || len(after) != 3 {
		t.Fatalf("layer counts: before=%d after=%d", len(before), len(after))
	}
	// alpha and beta layers must be unchanged.
	beforeSet := map[string]bool{before[0]: true, before[1]: true}
	matches := 0
	for _, d := range after {
		if beforeSet[d] {
			matches++
		}
	}
	if matches != 2 {
		t.Fatalf("expected 2 unchanged dependency layers, got %d (before=%v after=%v)", matches, before, after)
	}
}

func TestBuildWithCacheIsConsistentAndPopulates(t *testing.T) {
	dir := t.TempDir()
	wheels := []wheelhouse.ResolvedWheel{mkWheel(t, dir, "alpha", "1.0"), mkWheel(t, dir, "beta", "2.0")}

	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	opts := baseOpts(wheels)
	opts.Cache = c

	// Cold cache build.
	img1, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	d1, err := img1.Digest()
	if err != nil {
		t.Fatal(err)
	}

	// Warm cache build must produce an identical image.
	img2, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := img2.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("cached build digest differs: %s != %s", d1, d2)
	}

	// And it must match a build with no cache at all (cache changes nothing
	// about the output).
	img3, err := Build(baseOpts(wheels))
	if err != nil {
		t.Fatal(err)
	}
	d3, _ := img3.Digest()
	if d3 != d1 {
		t.Fatalf("cached vs uncached digest differ: %s != %s", d1, d3)
	}
}

// TestEnvPythonPathAccumulates ensures an extra PYTHONPATH (e.g. the app's
// "/app/src") is appended to the venv site-packages, not substituted for it —
// otherwise installed dependencies would be unimportable.
func TestEnvPythonPathAccumulates(t *testing.T) {
	dir := t.TempDir()
	wheels := []wheelhouse.ResolvedWheel{mkWheel(t, dir, "alpha", "1.0")}
	opts := baseOpts(wheels)
	opts.Env = []string{"PYTHONPATH=/app/src"}

	img, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	cf, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	var pythonPath string
	for _, kv := range cf.Config.Env {
		if k, v, _ := strings.Cut(kv, "="); k == "PYTHONPATH" {
			pythonPath = v
		}
	}
	site := "/app/.venv/lib/python3.12/site-packages"
	if !strings.Contains(pythonPath, site) {
		t.Errorf("PYTHONPATH %q missing site-packages %q", pythonPath, site)
	}
	if !strings.Contains(pythonPath, "/app/src") {
		t.Errorf("PYTHONPATH %q missing app path /app/src", pythonPath)
	}
	// site-packages must come first so installed deps take precedence.
	if !strings.HasPrefix(pythonPath, site) {
		t.Errorf("PYTHONPATH %q should start with site-packages", pythonPath)
	}
}

func TestInterpreterVersion(t *testing.T) {
	withEnv := func(env []string) v1.Image {
		img, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{Config: v1.Config{Env: env}})
		if err != nil {
			t.Fatal(err)
		}
		return img
	}

	if maj, min, ok := InterpreterVersion(withEnv([]string{"PYTHON_VERSION=3.12.11", "PATH=/usr/bin"})); !ok || maj != 3 || min != 12 {
		t.Errorf("got %d.%d ok=%v, want 3.12 ok=true", maj, min, ok)
	}
	if _, _, ok := InterpreterVersion(withEnv([]string{"PATH=/usr/bin"})); ok {
		t.Error("expected ok=false when no PYTHON_VERSION is advertised")
	}
	if maj, min, ok := InterpreterVersion(withEnv([]string{"PYTHON_VERSION=3.13"})); !ok || maj != 3 || min != 13 {
		t.Errorf("got %d.%d ok=%v, want 3.13 ok=true", maj, min, ok)
	}
}

func TestInterpreterVersionFromAPKO(t *testing.T) {
	// A Chainguard-style apko.json (no PYTHON_VERSION env) in the top layer.
	apko := `{"contents":{"packages":["ca-certificates-bundle=20260413-r0","py3-pip-wheel=26.1.2-r0","python-3.14-base=3.14.5-r2","python-3.14=3.14.5-r2","zlib=1.3.2-r3"]},"entrypoint":{"command":"/usr/bin/python"}}`
	layer, err := ptarLayer(t, "etc/apko.json", apko)
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}

	maj, min, ok := InterpreterVersion(img)
	if !ok || maj != 3 || min != 14 {
		t.Fatalf("apko fallback: got %d.%d ok=%v, want 3.14 ok=true", maj, min, ok)
	}

	// The PYTHON_VERSION env, when present, takes precedence over apko.json.
	cf, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	cf = cf.DeepCopy()
	cf.Config.Env = []string{"PYTHON_VERSION=3.12.7"}
	imgEnv, err := mutate.ConfigFile(img, cf)
	if err != nil {
		t.Fatal(err)
	}
	if maj, min, ok := InterpreterVersion(imgEnv); !ok || maj != 3 || min != 12 {
		t.Fatalf("env precedence: got %d.%d ok=%v, want 3.12", maj, min, ok)
	}
}

func TestSourceLayerIgnore(t *testing.T) {
	src := t.TempDir()
	must := func(p, content string) {
		full := filepath.Join(src, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("main.py", "x")
	must("pkg/__pycache__/main.cpython-312.pyc", "junk")
	must("pkg/mod.py", "y")
	must(".git/config", "secret")

	layer, err := sourceLayer(src, "/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	names := layerPaths(t, layer)

	want := map[string]bool{"app/main.py": true, "app/pkg/mod.py": true}
	for n := range want {
		if !names[n] {
			t.Errorf("expected %q in source layer", n)
		}
	}
	for n := range names {
		if strings.Contains(n, "__pycache__") || strings.Contains(n, ".git") || strings.HasSuffix(n, ".pyc") {
			t.Errorf("ignored path leaked into layer: %q", n)
		}
	}
}
