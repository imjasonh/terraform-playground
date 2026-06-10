// Package project discovers uv/Python project metadata for pymage defaults.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Info holds auto-detected project settings.
type Info struct {
	Root       string
	LockFile   string
	SourceDir  string
	Entrypoint []string
	ExtraEnv   []string // KEY=VALUE entries to add to the image
	Config     Config   // [tool.pymage] from pyproject.toml
}

// Config mirrors the [tool.pymage] table in pyproject.toml. Every field maps to
// a build flag of the same name; flags passed on the command line take
// precedence over these values, which take precedence over built-in defaults.
type Config struct {
	Repo          string            `toml:"repo"`
	Tags          []string          `toml:"tags"`
	Base          string            `toml:"base"`
	Platforms     []string          `toml:"platforms"`
	LayerStrategy string            `toml:"layer-strategy"`
	Python        string            `toml:"python"`
	Prefix        string            `toml:"prefix"`
	Workdir       string            `toml:"workdir"`
	User          string            `toml:"user"`
	Entrypoint    []string          `toml:"entrypoint"`
	Cmd           []string          `toml:"cmd"`
	Env           []string          `toml:"env"`
	Labels        map[string]string `toml:"labels"`
	FindLinks     []string          `toml:"find-links"`
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
			info.Entrypoint, info.ExtraEnv = entrypointFromPyproject(pp, abs)
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

func entrypointFromPyproject(pp pyProject, root string) ([]string, []string) {
	var extraEnv []string
	srcDir := filepath.Join(root, "src")
	if st, err := os.Stat(srcDir); err == nil && st.IsDir() {
		extraEnv = append(extraEnv, "PYTHONPATH=/app/src")
	}

	if len(pp.Project.Scripts) == 1 {
		for name := range pp.Project.Scripts {
			return []string{name}, extraEnv
		}
	}
	if len(pp.Project.Scripts) > 1 {
		// Prefer a script matching the project name.
		if pp.Project.Name != "" {
			if _, ok := pp.Project.Scripts[pp.Project.Name]; ok {
				return []string{pp.Project.Name}, extraEnv
			}
		}
		for name := range pp.Project.Scripts {
			return []string{name}, extraEnv
		}
	}

	pkg := pp.Project.Name
	if pkg == "" {
		pkg = "app"
	}
	if moduleDir(root, pkg) || srcModuleDir(root, pkg) {
		return []string{"python", "-m", pkg}, extraEnv
	}
	return nil, extraEnv
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

// DefaultWheelCacheDir returns the on-disk wheel download cache location.
func DefaultWheelCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pymage", "wheels"), nil
}
