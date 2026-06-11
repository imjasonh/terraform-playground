// Package project discovers uv/Python project metadata for pymage defaults.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Info holds auto-detected project settings.
type Info struct {
	Root       string
	LockFile   string
	SourceDir  string
	Entrypoint []string
	// SrcLayout is true when the project uses a top-level src/ directory, in
	// which case the caller should add "<workdir>/src" to PYTHONPATH (the path
	// depends on the configurable workdir, so it isn't baked here).
	SrcLayout bool
	Config    Config // [tool.pymage] from pyproject.toml
}

// Config mirrors the [tool.pymage] table in pyproject.toml. Every field maps to
// a build flag of the same name; flags passed on the command line take
// precedence over these values, which take precedence over built-in defaults.
type Config struct {
	Repo           string            `toml:"repo"`
	Tags           []string          `toml:"tags"`
	Base           string            `toml:"base"`
	Platforms      []string          `toml:"platforms"`
	LayerStrategy  string            `toml:"layer-strategy"`
	MaxLayers      int               `toml:"max-layers"`
	MaxWheelLayers int               `toml:"max-wheel-layers"`
	PushJobs       int               `toml:"push-concurrency"`
	Python         string            `toml:"python"`
	Prefix         string            `toml:"prefix"`
	Workdir        string            `toml:"workdir"`
	User           string            `toml:"user"`
	Entrypoint     []string          `toml:"entrypoint"`
	Cmd            []string          `toml:"cmd"`
	Env            []string          `toml:"env"`
	Labels         map[string]string `toml:"labels"`
	FindLinks      []string          `toml:"find-links"`
	NoCache        bool              `toml:"no-cache"`
	Extras         []string          `toml:"extras"`
	Package        string            `toml:"package"`
}

const defaultBase = "cgr.dev/chainguard/python:latest"

// DefaultBase is the base image used when --base is omitted.
func DefaultBase() string { return defaultBase }

// Discover inspects dir (usually ".") for a uv project and returns defaults.
func Discover(dir string) (Info, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Info{}, err
	}
	info := Info{Root: abs, SourceDir: abs}
	if st, err := os.Stat(filepath.Join(abs, "src")); err == nil && st.IsDir() {
		info.SrcLayout = true
	}

	lockNames := []string{"uv.lock", "requirements.lock", "requirements.txt"}
	for _, name := range lockNames {
		p := filepath.Join(abs, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			info.LockFile = p
			break
		}
	}
	if info.LockFile == "" {
		return info, fmt.Errorf("no uv.lock or requirements.txt in %s", abs)
	}

	pyproject := filepath.Join(abs, "pyproject.toml")
	if data, err := os.ReadFile(pyproject); err == nil {
		var pp pyProject
		if err := toml.Unmarshal(data, &pp); err == nil {
			info.Entrypoint = entrypointFromPyproject(pp, abs)
			info.Config = resolveConfig(pp.Tool.Pymage, abs)
		}
	}
	return info, nil
}

type pyProject struct {
	Project struct {
		Name    string            `toml:"name"`
		Scripts map[string]string `toml:"scripts"`
	} `toml:"project"`
	Tool struct {
		Pymage Config `toml:"pymage"`
	} `toml:"tool"`
}

// resolveConfig normalizes a parsed [tool.pymage] table, resolving any relative
// --find-links directories against the project root so they work regardless of
// the process's working directory.
func resolveConfig(c Config, root string) Config {
	for i, fl := range c.FindLinks {
		if fl != "" && !filepath.IsAbs(fl) {
			c.FindLinks[i] = filepath.Join(root, fl)
		}
	}
	return c
}

func entrypointFromPyproject(pp pyProject, root string) []string {
	// A [project.scripts] console script is the preferred entrypoint, but
	// pymage copies the app *source* (it doesn't install the project as a
	// wheel), so the launcher binary the script name refers to does not exist
	// in the image. Translate the script's "module:attr" target into a direct
	// `python` invocation instead, which works as long as the package is
	// importable (it is: via PYTHONPATH for src layouts, or the workdir).
	if _, target, ok := chooseScript(pp); ok {
		if ep := scriptEntrypoint(target); ep != nil {
			return ep
		}
	}

	pkg := pp.Project.Name
	if pkg == "" {
		pkg = "app"
	}
	if moduleDir(root, pkg) || srcModuleDir(root, pkg) {
		return []string{"python", "-m", pkg}
	}
	return nil
}

// chooseScript selects a [project.scripts] entry deterministically: the one
// matching the project name if present, else the lexicographically first.
func chooseScript(pp pyProject) (name, target string, ok bool) {
	scripts := pp.Project.Scripts
	if len(scripts) == 0 {
		return "", "", false
	}
	if pp.Project.Name != "" {
		if t, ok := scripts[pp.Project.Name]; ok {
			return pp.Project.Name, t, true
		}
	}
	names := make([]string, 0, len(scripts))
	for n := range scripts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names[0], scripts[names[0]], true
}

// scriptEntrypoint turns a console-script target ("module:attr" or "module")
// into an entrypoint that runs without a pre-installed launcher on PATH.
func scriptEntrypoint(target string) []string {
	module, attr, hasAttr := strings.Cut(target, ":")
	module = strings.TrimSpace(module)
	if module == "" {
		return nil
	}
	attr = strings.TrimSpace(attr)
	if !hasAttr || attr == "" {
		return []string{"python", "-m", module}
	}
	root := attr
	if i := strings.IndexByte(attr, '.'); i >= 0 {
		root = attr[:i]
	}
	return []string{"python", "-c", fmt.Sprintf("import sys; from %s import %s; sys.exit(%s())", module, root, attr)}
}

func moduleDir(root, pkg string) bool {
	p := filepath.Join(root, strings.ReplaceAll(pkg, ".", string(filepath.Separator)))
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func srcModuleDir(root, pkg string) bool {
	p := filepath.Join(root, "src", strings.ReplaceAll(pkg, ".", string(filepath.Separator)))
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// DefaultCacheDir returns pymage's cache root: $PYMAGE_CACHE_DIR if set,
// otherwise a "pymage" directory under the per-user cache dir.
func DefaultCacheDir() (string, error) {
	if v := os.Getenv("PYMAGE_CACHE_DIR"); v != "" {
		return v, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pymage"), nil
}

// DefaultWheelCacheDir returns the on-disk wheel download cache location.
func DefaultWheelCacheDir() (string, error) {
	dir, err := DefaultCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wheels"), nil
}
