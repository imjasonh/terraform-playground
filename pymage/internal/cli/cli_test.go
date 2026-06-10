package cli

import (
	"bytes"
	"context"
	"fmt"
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

	"github.com/imjasonh/terraform-playground/pymage/internal/testwheel"
)

// TestBuildMultiArchIndex drives the CLI to build a linux/amd64 + linux/arm64
// image index from a single host, and verifies the pushed artifact is an index
// covering both platforms and is reproducible.
func TestBuildMultiArchIndex(t *testing.T) {
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")

	// A multi-arch base (linux/amd64 + linux/arm64), as a real registry would
	// serve python:3.12-slim.
	baseRef := host + "/base:latest"
	writeMultiArchBase(t, baseRef, []string{"amd64", "arm64"})

	// Pure-python wheels are compatible with every platform.
	wh := t.TempDir()
	_, shaA := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1.0'\n"}})
	reqDir := t.TempDir()
	reqFile := filepath.Join(reqDir, "requirements.txt")
	if err := os.WriteFile(reqFile, []byte(fmt.Sprintf("alpha==1.0 --hash=sha256:%s\n", shaA)), 0o644); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "app.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	build := func(tag string) error {
		cmd := Root()
		cmd.SetArgs([]string{
			"build", src,
			"--base", baseRef,
			"--lock", reqFile,
			"--find-links", wh,
			"--platform", "linux/amd64,linux/arm64",
			"--entrypoint", "python", "--entrypoint", "/app/app.py",
			"--repo", host + "/multi",
			"-t", tag,
		})
		cmd.SetOut(os.Stderr)
		return cmd.Execute()
	}

	if err := build("v1"); err != nil {
		t.Fatalf("multi-arch build failed: %v", err)
	}

	ref, _ := name.ParseReference(host + "/multi:v1")
	idx, err := remote.Index(ref, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatalf("pulled artifact is not an index: %v", err)
	}
	im, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range im.Manifests {
		if m.Platform != nil {
			got[m.Platform.OS+"/"+m.Platform.Architecture] = true
		}
	}
	for _, want := range []string{"linux/amd64", "linux/arm64"} {
		if !got[want] {
			t.Errorf("index missing platform %s; got %v", want, got)
		}
	}
	if len(im.Manifests) != 2 {
		t.Fatalf("expected 2 manifests, got %d", len(im.Manifests))
	}
	d1, _ := idx.Digest()

	// Reproducible: a second build yields the same index digest.
	if err := build("v2"); err != nil {
		t.Fatal(err)
	}
	ref2, _ := name.ParseReference(host + "/multi:v2")
	idx2, err := remote.Index(ref2, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := idx2.Digest()
	if d1 != d2 {
		t.Fatalf("multi-arch index not reproducible: %s != %s", d1, d2)
	}
}

// TestBuildDefaultsToBasePlatforms verifies that, without --platform, the build
// targets exactly the platforms the base image advertises.
func TestBuildDefaultsToBasePlatforms(t *testing.T) {
	ctx := context.Background()
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")

	// Multi-arch base (amd64 + arm64), advertising its Python version.
	baseRef := host + "/base:latest"
	writeMultiArchBase(t, baseRef, []string{"amd64", "arm64"})

	wh := t.TempDir()
	_, sha := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1'\n"}})
	reqFile := filepath.Join(t.TempDir(), "requirements.txt")
	if err := os.WriteFile(reqFile, []byte(fmt.Sprintf("alpha==1.0 --hash=sha256:%s\n", sha)), 0o644); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "app.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := Root()
	cmd.SetArgs([]string{
		"build", src,
		"--base", baseRef,
		"--lock", reqFile,
		"--find-links", wh,
		"--entrypoint", "python", "--entrypoint", "/app/app.py",
		"--repo", host + "/app",
		// no --platform: should default to the base's amd64 + arm64
	})
	cmd.SetOut(os.Stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build failed: %v", err)
	}

	ref, _ := name.ParseReference(host + "/app:latest")
	idx, err := remote.Index(ref, remote.WithContext(ctx))
	if err != nil {
		t.Fatalf("expected a multi-arch index by default: %v", err)
	}
	im, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range im.Manifests {
		if m.Platform != nil {
			got[m.Platform.OS+"/"+m.Platform.Architecture] = true
		}
	}
	if len(im.Manifests) != 2 || !got["linux/amd64"] || !got["linux/arm64"] {
		t.Fatalf("default index platforms = %v, want linux/amd64 + linux/arm64", got)
	}
}

func writeMultiArchBase(t *testing.T, ref string, arches []string) {
	t.Helper()
	var adds []mutate.IndexAddendum
	for _, arch := range arches {
		img, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{
			OS:           "linux",
			Architecture: arch,
			Config:       v1.Config{Env: []string{"PYTHON_VERSION=3.12.7", "PATH=/usr/local/bin:/usr/bin:/bin"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		adds = append(adds, mutate.IndexAddendum{
			Add:        img,
			Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: arch}},
		})
	}
	idx := mutate.AppendManifests(empty.Index, adds...)
	r, _ := name.ParseReference(ref)
	if err := remote.WriteIndex(r, idx, remote.WithContext(context.Background())); err != nil {
		t.Fatalf("write base index: %v", err)
	}
}

// TestBuildRejectsInterpreterMismatch ensures a base advertising a different
// Python version than --python fails fast (the floating-tag drift guard).
func TestBuildRejectsInterpreterMismatch(t *testing.T) {
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")

	baseRef := host + "/base:py313"
	img, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{
		OS: "linux", Architecture: "amd64",
		Config: v1.Config{Env: []string{"PYTHON_VERSION=3.13.1", "PATH=/usr/bin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := name.ParseReference(baseRef)
	if err := remote.Write(r, img, remote.WithContext(context.Background())); err != nil {
		t.Fatal(err)
	}

	wh := t.TempDir()
	_, sha := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1'\n"}})
	reqFile := filepath.Join(t.TempDir(), "requirements.txt")
	if err := os.WriteFile(reqFile, []byte(fmt.Sprintf("alpha==1.0 --hash=sha256:%s\n", sha)), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := Root()
	src := t.TempDir()
	cmd.SetArgs([]string{
		"build", src,
		"--base", baseRef,
		"--lock", reqFile,
		"--find-links", wh,
		"--entrypoint", "python", "--entrypoint", "/app/app.py",
		"--python", "python3.12", // mismatches the base's 3.13
		"--push=false",
		"--print-digest",
	})
	cmd.SetOut(os.Stderr)
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected build to fail on interpreter mismatch")
	}
	if !strings.Contains(err.Error(), "Python 3.13") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBuildUsesPyprojectConfig drives the CLI with no --repo/--base/--platform
// flags, proving they are sourced from [tool.pymage] in pyproject.toml.
func TestBuildUsesPyprojectConfig(t *testing.T) {
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")

	baseRef := host + "/base:latest"
	writeMultiArchBase(t, baseRef, []string{"amd64", "arm64"})

	// A self-contained uv-style project: pyproject (with [tool.pymage]),
	// requirements lock, a wheelhouse, and a console script entrypoint.
	root := t.TempDir()
	wh := filepath.Join(root, "wheelhouse")
	if err := os.MkdirAll(wh, 0o755); err != nil {
		t.Fatal(err)
	}
	_, shaA := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1.0'\ndef main():\n    pass\n"}})
	if err := os.WriteFile(filepath.Join(root, "requirements.txt"),
		[]byte(fmt.Sprintf("alpha==1.0 --hash=sha256:%s\n", shaA)), 0o644); err != nil {
		t.Fatal(err)
	}
	pyproject := fmt.Sprintf(`[project]
name = "demo"
version = "0.1.0"

[project.scripts]
demo = "alpha:main"

[tool.pymage]
repo = %q
base = %q
platforms = ["linux/amd64", "linux/arm64"]
tags = ["release", "latest"]
`, host+"/fromconfig", baseRef)
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := Root()
	cmd.SetArgs([]string{
		"build", root,
		"--lock", filepath.Join(root, "requirements.txt"),
		"--find-links", wh,
		"--insecure",
	})
	cmd.SetOut(os.Stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config-driven build failed: %v", err)
	}

	// Both configured tags resolve to the same multi-arch index at the
	// configured repo.
	relRef, _ := name.ParseReference(host+"/fromconfig:release", name.Insecure)
	latestRef, _ := name.ParseReference(host+"/fromconfig:latest", name.Insecure)
	relIdx, err := remote.Index(relRef, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatalf("pull release tag from configured repo: %v", err)
	}
	im, err := relIdx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(im.Manifests) != 2 {
		t.Fatalf("expected 2 platform manifests from config, got %d", len(im.Manifests))
	}
	relDigest, _ := relIdx.Digest()
	latestIdx, err := remote.Index(latestRef, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatalf("pull latest tag: %v", err)
	}
	latestDigest, _ := latestIdx.Digest()
	if relDigest != latestDigest {
		t.Fatalf("configured tags point to different digests: %s != %s", relDigest, latestDigest)
	}
}

// TestPushRejectsFullReferenceTag ensures -t is a tag only; a full reference
// (with a registry/repo) is rejected so it can't be confused with --repo.
func TestPushRejectsFullReferenceTag(t *testing.T) {
	err := validateBuildFlags(&buildFlags{
		lockFile:   "x",
		entrypoint: []string{"app"},
		push:       true,
		repo:       "gcr.io/foo/bar",
		tags:       []string{"registry.example.com/me/app:v1"},
	})
	if err == nil || !strings.Contains(err.Error(), "tag only") {
		t.Fatalf("expected tag-only rejection, got %v", err)
	}
}

// TestPushRequiresRepo ensures a push with no repo configured fails clearly.
func TestPushRequiresRepo(t *testing.T) {
	err := validateBuildFlags(&buildFlags{
		lockFile:   "x",
		entrypoint: []string{"app"},
		push:       true,
	})
	if err == nil || !strings.Contains(err.Error(), "repo") {
		t.Fatalf("expected missing-repo error, got %v", err)
	}
}

// TestBuildStdoutIsImageRef checks the contract that `pymage build` prints
// exactly the pullable by-digest reference to stdout (so `docker run
// "$(pymage build)"` works), while progress/diagnostics — including per-blob
// push/skip logs and per-tag lines — go to stderr.
func TestBuildStdoutIsImageRef(t *testing.T) {
	ctx := context.Background()
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")

	baseRef := host + "/base:latest"
	base, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{
		OS: "linux", Architecture: "amd64",
		Config: v1.Config{Env: []string{"PYTHON_VERSION=3.12.7", "PATH=/usr/bin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	br, _ := name.ParseReference(baseRef)
	if err := remote.Write(br, base, remote.WithContext(ctx)); err != nil {
		t.Fatal(err)
	}

	wh := t.TempDir()
	_, sha := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1'\n"}})
	reqFile := filepath.Join(t.TempDir(), "requirements.txt")
	if err := os.WriteFile(reqFile, []byte(fmt.Sprintf("alpha==1.0 --hash=sha256:%s\n", sha)), 0o644); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "app.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cmd := Root()
	cmd.SetArgs([]string{
		"build", src,
		"--base", baseRef,
		"--lock", reqFile,
		"--find-links", wh,
		"--entrypoint", "python", "--entrypoint", "/app/app.py",
		"--repo", host + "/app",
		"-t", "v1", "-t", "v2",
	})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("build failed: %v\nstderr:\n%s", err, stderr.String())
	}

	out := strings.TrimRight(stdout.String(), "\n")
	if strings.Contains(out, "\n") {
		t.Fatalf("stdout must be a single line, got:\n%q", out)
	}
	if !strings.HasPrefix(out, host+"/app@sha256:") {
		t.Fatalf("stdout = %q, want %q@sha256:...", out, host+"/app")
	}

	// The printed ref must be pullable and its digest must match the image.
	ref, err := name.ParseReference(out)
	if err != nil {
		t.Fatalf("parse stdout ref %q: %v", out, err)
	}
	img, err := remote.Image(ref, remote.WithContext(ctx))
	if err != nil {
		t.Fatalf("pull %q: %v", out, err)
	}
	d, _ := img.Digest()
	if !strings.HasSuffix(out, d.String()) {
		t.Errorf("stdout ref %q does not end with image digest %s", out, d)
	}

	// stderr should carry the per-tag pointers and ggcr's per-blob progress.
	se := stderr.String()
	for _, want := range []string{"tagged", "v1", "v2", "blob"} {
		if !strings.Contains(se, want) {
			t.Errorf("stderr missing %q; got:\n%s", want, se)
		}
	}
}

func TestPushConcurrency(t *testing.T) {
	if got := pushConcurrency(7); got != 7 {
		t.Errorf("explicit value not honored: got %d, want 7", got)
	}
	if got := pushConcurrency(0); got < 4 {
		t.Errorf("auto concurrency = %d, want >= 4", got)
	}
}

func TestParsePythonTag(t *testing.T) {
	maj, min, err := parsePythonTag("python3.12")
	if err != nil || maj != 3 || min != 12 {
		t.Fatalf("parsePythonTag = %d.%d, %v", maj, min, err)
	}
	if _, _, err := parsePythonTag("python3"); err == nil {
		t.Error("expected error for missing minor version")
	}
}

func TestPlatformTargetDefaults(t *testing.T) {
	if tg := platformTarget(nil); tg.OS != "linux" || tg.Arch != "amd64" {
		t.Fatalf("unexpected default target %+v", tg)
	}
	tg := platformTarget(&v1.Platform{OS: "linux", Architecture: "arm64"})
	if tg.OS != "linux" || tg.Arch != "arm64" {
		t.Fatalf("expected linux/arm64, got %+v", tg)
	}
}

func TestResolveInterpreter(t *testing.T) {
	withVersion := func(env []string) v1.Image {
		img, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{Config: v1.Config{Env: env}})
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	base313 := withVersion([]string{"PYTHON_VERSION=3.13.1"})
	bareBase := withVersion(nil)

	// Auto-detect when --python is omitted.
	maj, min, tag, err := resolveInterpreter(&buildFlags{}, base313)
	if err != nil || maj != 3 || min != 13 || tag != "python3.13" {
		t.Fatalf("auto-detect: %d.%d %q err=%v", maj, min, tag, err)
	}

	// Explicit --python that matches is accepted.
	if _, _, tag, err := resolveInterpreter(&buildFlags{pythonTag: "python3.13"}, base313); err != nil || tag != "python3.13" {
		t.Fatalf("matching --python: %q err=%v", tag, err)
	}

	// Explicit --python that mismatches the base is rejected.
	if _, _, _, err := resolveInterpreter(&buildFlags{pythonTag: "python3.12"}, base313); err == nil {
		t.Fatal("expected mismatch error")
	}

	// No --python and an undetectable base is an error.
	if _, _, _, err := resolveInterpreter(&buildFlags{}, bareBase); err == nil {
		t.Fatal("expected error when base version is unknown and --python is unset")
	}

	// Explicit --python works even when the base can't be validated.
	if _, _, tag, err := resolveInterpreter(&buildFlags{pythonTag: "python3.10"}, bareBase); err != nil || tag != "python3.10" {
		t.Fatalf("explicit on bare base: %q err=%v", tag, err)
	}
}

func TestEnvValidation(t *testing.T) {
	if _, err := keyValues([]string{"GOOD=1"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := keyValues([]string{"NOEQUALS"}); err == nil {
		t.Error("expected error for env entry without '='")
	}
	if _, err := keyValues([]string{"=value"}); err == nil {
		t.Error("expected error for env entry with empty key")
	}
}

// TestBuildCommandEndToEnd drives the cobra `build` command exactly as a user
// would: a hashed requirements file + a wheelhouse + source, pushed to a local
// registry, then verifies the pushed image is pullable and reproducible.
func TestBuildCommandEndToEnd(t *testing.T) {
	s := httptest.NewServer(registry.New())
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://") // 127.0.0.1:port -> ggcr uses http

	// Seed a base image that advertises its Python version, so the build can
	// auto-detect the interpreter (no --python flag below).
	baseRef := host + "/base:latest"
	base, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{
		OS: "linux", Architecture: "amd64",
		Config: v1.Config{Env: []string{"PYTHON_VERSION=3.12.7", "PATH=/usr/bin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	br, err := name.ParseReference(baseRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(br, base, remote.WithContext(context.Background())); err != nil {
		t.Fatal(err)
	}

	// Wheelhouse with two wheels.
	wh := t.TempDir()
	_, shaA := testwheel.Write(t, wh, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha/__init__.py": "V='1.0'\n"}})
	_, shaB := testwheel.Write(t, wh, testwheel.Spec{Name: "beta", Version: "2.0", Modules: map[string]string{"beta/__init__.py": "V='2.0'\n"}})

	// Hashed requirements file.
	reqDir := t.TempDir()
	reqFile := filepath.Join(reqDir, "requirements.txt")
	reqs := fmt.Sprintf("alpha==1.0 --hash=sha256:%s\nbeta==2.0 --hash=sha256:%s\n", shaA, shaB)
	if err := os.WriteFile(reqFile, []byte(reqs), 0o644); err != nil {
		t.Fatal(err)
	}

	// App source.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "app.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tag := host + "/myapp:v1"
	run := func() error {
		cmd := Root()
		cmd.SetArgs([]string{
			"build", src,
			"--base", baseRef,
			"--lock", reqFile,
			"--find-links", wh,
			"--entrypoint", "python",
			"--entrypoint", "/app/app.py",
			"--repo", host + "/myapp",
			"-t", "v1",
		})
		cmd.SetOut(os.Stderr)
		return cmd.Execute()
	}

	if err := run(); err != nil {
		t.Fatalf("build command failed: %v", err)
	}

	// The pushed image is pullable and has the expected layer count
	// (2 wheels + 1 source).
	ref, _ := name.ParseReference(tag)
	img, err := remote.Image(ref, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatalf("pull pushed image: %v", err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Fatalf("got %d layers, want 3", len(layers))
	}

	// The interpreter was auto-detected (3.12) from the base and used for the
	// site-packages layout.
	cf, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cf.Config.Env, "\n"), "lib/python3.12/site-packages") {
		t.Errorf("expected auto-detected python3.12 site-packages in env, got %v", cf.Config.Env)
	}
	pushedDigest, _ := img.Digest()

	// Running the command again is reproducible: same digest.
	if err := run(); err != nil {
		t.Fatalf("second build failed: %v", err)
	}
	img2, err := remote.Image(ref, remote.WithContext(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := img2.Digest()
	if d2 != pushedDigest {
		t.Fatalf("re-running build produced a different digest: %s != %s", d2, pushedDigest)
	}
}
