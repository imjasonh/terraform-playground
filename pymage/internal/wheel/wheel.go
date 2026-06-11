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
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
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

// MaxUncompressedFile caps the size of any single wheel member we extract, as
// a guard against decompression bombs in untrusted wheels.
const MaxUncompressedFile = 1 << 30 // 1 GiB

// Target describes the image we are building for: a platform and an interpreter
// version. It is used to decide whether a wheel's compatibility tags apply.
type Target struct {
	OS      string // e.g. "linux"
	Arch    string // e.g. "amd64", "arm64"
	PyMajor int    // e.g. 3
	PyMinor int    // e.g. 12
}

// archAlias maps Go-style architectures to the tokens used in wheel platform
// tags (e.g. amd64 -> x86_64).
var archAlias = map[string]string{
	"amd64":   "x86_64",
	"arm64":   "aarch64",
	"386":     "i686",
	"arm":     "armv7l",
	"ppc64le": "ppc64le",
	"s390x":   "s390x",
}

// CompatibleWith reports whether a wheel with these tags can be installed for
// the target. It evaluates the cross product of the compressed tag sets.
func (t Tags) CompatibleWith(tg Target) bool {
	for _, plat := range strings.Split(t.Platform, ".") {
		if !platformCompatible(plat, tg) {
			continue
		}
		for _, py := range strings.Split(t.Python, ".") {
			for _, abi := range strings.Split(t.ABI, ".") {
				if pyABICompatible(py, abi, tg) {
					return true
				}
			}
		}
	}
	return false
}

func platformCompatible(plat string, tg Target) bool {
	if plat == "any" {
		return true
	}
	if tg.OS != "linux" {
		// We only model linux image platforms precisely; anything else only
		// matches platform-independent ("any") wheels.
		return false
	}
	alias := archAlias[tg.Arch]
	if alias == "" {
		return false
	}
	if !strings.HasPrefix(plat, "manylinux") && !strings.HasPrefix(plat, "musllinux") && !strings.HasPrefix(plat, "linux") {
		return false
	}
	return strings.HasSuffix(plat, "_"+alias)
}

func pyABICompatible(py, abi string, tg Target) bool {
	pyX := fmt.Sprintf("py%d", tg.PyMajor)
	pyXY := fmt.Sprintf("py%d%d", tg.PyMajor, tg.PyMinor)
	cpXY := fmt.Sprintf("cp%d%d", tg.PyMajor, tg.PyMinor)

	switch abi {
	case "none":
		// Pure-python (or platform-only) wheels.
		return py == pyX || py == pyXY || py == cpXY
	case "abi3":
		// Stable ABI: a cp3X-abi3 wheel works on any CPython >= 3.X.
		maj, min, ok := parseCPython(py)
		return ok && maj == tg.PyMajor && min <= tg.PyMinor
	default:
		// Version-specific ABI, e.g. cp312.
		return abi == cpXY
	}
}

func parseCPython(tag string) (maj, min int, ok bool) {
	if !strings.HasPrefix(tag, "cp") || len(tag) < 4 {
		return 0, 0, false
	}
	rest := tag[2:]
	maj = int(rest[0] - '0')
	if maj < 0 || maj > 9 {
		return 0, 0, false
	}
	min, err := strconv.Atoi(rest[1:])
	if err != nil {
		return 0, 0, false
	}
	return maj, min, true
}

// Tags holds the compatibility tags from a wheel filename (PEP 427). Each of
// Python/ABI/Platform may be a compressed set joined by ".", e.g. py2.py3.
type Tags struct {
	Name     string
	Version  string
	Build    string // optional build tag (e.g. "1"), empty if absent
	Python   string // pytag, e.g. "py3" or "cp312"
	ABI      string // abitag, e.g. "none", "abi3", "cp312"
	Platform string // plattag, e.g. "any" or "manylinux2014_x86_64"
}

// BuildRank returns the integer prefix of the build tag (0 if absent), used to
// prefer the highest build among otherwise-equivalent wheels.
func (t Tags) BuildRank() int {
	if t.Build == "" {
		return 0
	}
	i := 0
	for i < len(t.Build) && t.Build[i] >= '0' && t.Build[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, err := strconv.Atoi(t.Build[:i])
	if err != nil {
		return 0
	}
	return n
}

// ParseFilename extracts the distribution name and version from a wheel
// filename per PEP 427: name-version(-build)?-pytag-abitag-plattag.whl.
func ParseFilename(filename string) (name, version string, err error) {
	t, err := ParseTags(filename)
	if err != nil {
		return "", "", err
	}
	return t.Name, t.Version, nil
}

// ParseTags parses a wheel filename into name, version, and compatibility tags.
func ParseTags(filename string) (Tags, error) {
	base := strings.TrimSuffix(path.Base(filename), ".whl")
	parts := strings.Split(base, "-")
	// name-version(-build)?-pytag-abitag-plattag
	if len(parts) < 5 {
		return Tags{}, fmt.Errorf("wheel: malformed filename %q", base)
	}
	n := len(parts)
	plat, abi, py := parts[n-1], parts[n-2], parts[n-3]
	head := parts[:n-3] // name, version, (build)
	build := ""
	if len(head) >= 3 {
		build = head[2]
	}
	return Tags{
		Name:     head[0],
		Version:  head[1],
		Build:    build,
		Python:   py,
		ABI:      abi,
		Platform: plat,
	}, nil
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
		// Reject any ".." segment outright: even a member that resolves back
		// under the prefix (e.g. "foo/../../bin/x") could escape site-packages
		// and clobber other installed files. within() below is defense in depth.
		if hasDotDotSegment(f.Name) {
			return nil, fmt.Errorf("wheel: member %q contains a %q path segment", f.Name, "..")
		}
		dst, exec, skip := w.dest(f.Name, site, bin, prefix)
		if skip {
			continue
		}
		// Defend against malicious wheels whose member names escape the install
		// prefix via "..": every installed file must land under the prefix.
		if !within(prefix, dst) {
			return nil, fmt.Errorf("wheel: member %q would install outside the prefix (at %q)", f.Name, dst)
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

	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// hasDotDotSegment reports whether any "/"- or "\"-separated segment of name is
// "..", which would let a wheel member traverse out of its install directory.
func hasDotDotSegment(name string) bool {
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// within reports whether child is parent itself or nested under it.
func within(parent, child string) bool {
	parent = path.Clean(parent)
	child = path.Clean(child)
	return child == parent || strings.HasPrefix(child, parent+"/")
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
	// The file is already fully in memory, so split directly rather than using a
	// bufio.Scanner (whose default token limit would silently drop long lines).
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
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
		// Entry-point targets may carry an optional "[extras]" suffix
		// (e.g. "pkg.mod:main [cli]"); it is not part of the import path and
		// must be stripped or the generated launcher is invalid Python.
		if i := strings.IndexByte(target, '['); i >= 0 {
			target = strings.TrimSpace(target[:i])
		}
		module, attr, _ := strings.Cut(target, ":")
		module = strings.TrimSpace(module)
		attr = strings.TrimSpace(attr)
		if module == "" {
			// Without a module we cannot generate a valid launcher.
			continue
		}
		if attr == "" {
			attr = "main"
		}
		out[name] = entryPoint{module: module, attr: attr}
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
	if f.UncompressedSize64 > MaxUncompressedFile {
		return nil, fmt.Errorf("wheel: member %q is %d bytes, exceeds limit of %d", f.Name, f.UncompressedSize64, MaxUncompressedFile)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	// LimitReader guards against a lying header (declared size < actual).
	data, err := io.ReadAll(io.LimitReader(rc, MaxUncompressedFile+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxUncompressedFile {
		return nil, fmt.Errorf("wheel: member %q exceeds limit of %d bytes", f.Name, MaxUncompressedFile)
	}
	return data, nil
}

func isExec(f *zip.File) bool {
	return f.FileInfo().Mode().Perm()&0o111 != 0
}

func trimSlash(s string) string { return strings.TrimPrefix(path.Clean(s), "/") }
