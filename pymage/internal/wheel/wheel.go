// Package wheel parses Python wheel files (PEP 427) and turns each wheel into a
// deterministic set of installed files suitable for a single OCI layer.
//
// "Installing" a wheel is deliberately simple and byte-stable: a wheel is a zip
// archive whose members are laid down relative to a target prefix. Most members
// go straight into site-packages; members under the "<name>-<ver>.data"
// directory are redirected (scripts -> bin, purelib/platlib -> site-packages,
// data -> prefix root). We then synthesize console-script entry points.
//
// We intentionally do NOT byte-compile (no __pycache__/*.pyc) because compiled
// output is not reliably reproducible across interpreter patch releases; the
// interpreter compiles on first import at runtime instead.
package wheel

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/imjasonh/terraform-playground/pymage/internal/ptar"
)

// Layout describes where files are installed inside the image filesystem.
type Layout struct {
	// Prefix is the virtualenv-like root, e.g. "/app/.venv".
	Prefix string
	// PythonTag is the interpreter dir under lib, e.g. "python3.12".
	PythonTag string
}

// SitePackages returns the slash path (no leading slash) of site-packages.
func (l Layout) SitePackages() string {
	return path.Join(trimSlash(l.Prefix), "lib", l.PythonTag, "site-packages")
}

// BinDir returns the slash path (no leading slash) of the scripts/bin dir.
func (l Layout) BinDir() string {
	return path.Join(trimSlash(l.Prefix), "bin")
}

// Wheel is a parsed wheel file.
type Wheel struct {
	// Name is the distribution name as it appears in the filename (with
	// underscores), e.g. "Flask".
	Name string
	// Version is the distribution version.
	Version string
	// distInfo is the "<name>-<ver>.dist-info" directory name.
	distInfo string
	// dataDir is the "<name>-<ver>.data" directory name.
	dataDir string

	z *zip.ReadCloser
}

// ParseFilename extracts the distribution name and version from a wheel
// filename per PEP 427: name-version(-build)?-pytag-abitag-plattag.whl.
func ParseFilename(filename string) (name, version string, err error) {
	base := path.Base(filename)
	parts := strings.Split(strings.TrimSuffix(base, ".whl"), "-")
	if len(parts) < 5 {
		return "", "", fmt.Errorf("wheel: malformed filename %q", base)
	}
	// The trailing 3 fields are pytag, abitag, plattag. An optional build tag
	// may sit before them; name/version are everything before that.
	// name is parts[0], version is parts[1]; build/tag fields follow.
	return parts[0], parts[1], nil
}

// Open reads and parses a wheel file from disk.
func Open(filename string) (*Wheel, error) {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("wheel: open %q: %w", filename, err)
	}
	w := &Wheel{z: z}
	for _, f := range z.File {
		top := strings.SplitN(f.Name, "/", 2)[0]
		switch {
		case strings.HasSuffix(top, ".dist-info") && w.distInfo == "":
			w.distInfo = top
		case strings.HasSuffix(top, ".data") && w.dataDir == "":
			w.dataDir = top
		}
	}
	if w.distInfo == "" {
		_ = z.Close()
		return nil, fmt.Errorf("wheel: %q has no .dist-info directory", filename)
	}
	base := strings.TrimSuffix(w.distInfo, ".dist-info")
	if i := strings.LastIndex(base, "-"); i >= 0 {
		w.Name, w.Version = base[:i], base[i+1:]
	} else {
		w.Name = base
	}
	return w, nil
}

// Close releases the underlying zip reader.
func (w *Wheel) Close() error { return w.z.Close() }

// Files returns the deterministic set of installed files for this wheel under
// the given layout.
func (w *Wheel) Files(layout Layout) ([]ptar.File, error) {
	site := layout.SitePackages()
	bin := layout.BinDir()
	prefix := trimSlash(layout.Prefix)

	var out []ptar.File
	for _, f := range w.z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		dst, exec, skip := w.dest(f.Name, site, bin, prefix)
		if skip {
			continue
		}
		data, err := readZip(f)
		if err != nil {
			return nil, err
		}
		out = append(out, ptar.File{Path: dst, Data: data, Executable: exec || isExec(f)})
	}

	scripts, err := w.consoleScripts(bin)
	if err != nil {
		return nil, err
	}
	out = append(out, scripts...)

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// dest maps a wheel member name to its install destination. It handles the
// special "<name>-<ver>.data/{scripts,purelib,platlib,data,headers}" tree.
func (w *Wheel) dest(name, site, bin, prefix string) (dst string, exec, skip bool) {
	if w.dataDir != "" && strings.HasPrefix(name, w.dataDir+"/") {
		rest := strings.TrimPrefix(name, w.dataDir+"/")
		category, sub, ok := strings.Cut(rest, "/")
		if !ok {
			return "", false, true
		}
		switch category {
		case "scripts":
			return path.Join(bin, sub), true, false
		case "purelib", "platlib":
			return path.Join(site, sub), false, false
		case "data":
			return path.Join(prefix, sub), false, false
		default: // headers and anything else: skip for simplicity
			return "", false, true
		}
	}
	return path.Join(site, name), false, false
}

// consoleScripts synthesizes launcher scripts for [console_scripts] entry
// points declared in entry_points.txt.
func (w *Wheel) consoleScripts(bin string) ([]ptar.File, error) {
	f := w.fileByName(w.distInfo + "/entry_points.txt")
	if f == nil {
		return nil, nil
	}
	data, err := readZip(f)
	if err != nil {
		return nil, err
	}
	eps := parseConsoleScripts(string(data))
	names := make([]string, 0, len(eps))
	for k := range eps {
		names = append(names, k)
	}
	sort.Strings(names)

	var out []ptar.File
	for _, n := range names {
		module, attr := eps[n].module, eps[n].attr
		script := fmt.Sprintf(`#!/usr/bin/env python
# Generated by pymage.
import sys
from %s import %s
if __name__ == "__main__":
    sys.exit(%s())
`, module, rootAttr(attr), attr)
		out = append(out, ptar.File{Path: path.Join(bin, n), Data: []byte(script), Executable: true})
	}
	return out, nil
}

type entryPoint struct{ module, attr string }

// parseConsoleScripts parses the [console_scripts] section of an
// entry_points.txt file into name -> {module, attr}.
func parseConsoleScripts(s string) map[string]entryPoint {
	out := map[string]entryPoint{}
	section := ""
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			continue
		}
		if section != "console_scripts" {
			continue
		}
		name, target, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		target = strings.TrimSpace(target)
		module, attr, _ := strings.Cut(target, ":")
		if attr == "" {
			attr = "main"
		}
		out[name] = entryPoint{module: strings.TrimSpace(module), attr: strings.TrimSpace(attr)}
	}
	return out
}

// rootAttr returns the top-level attribute name (before any dotted access) so
// the generated `from module import <root>` is valid.
func rootAttr(attr string) string {
	if i := strings.Index(attr, "."); i >= 0 {
		return attr[:i]
	}
	return attr
}

func (w *Wheel) fileByName(name string) *zip.File {
	for _, f := range w.z.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func readZip(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func isExec(f *zip.File) bool {
	return f.FileInfo().Mode().Perm()&0o111 != 0
}

func trimSlash(s string) string { return strings.TrimPrefix(path.Clean(s), "/") }
