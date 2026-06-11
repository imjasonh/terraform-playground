// Package lock parses fully-pinned, hashed requirements files (the output of
// `pip-compile` / `uv pip compile --generate-hashes`).
//
// We deliberately do not resolve dependencies ourselves; we consume a lock that
// already pins every distribution to an exact version with one or more sha256
// hashes. The hashes are what make a build reproducible and let us key the
// layer cache by wheel content.
package lock

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// WheelRef is a pinned wheel distribution from a lockfile (e.g. uv.lock).
type WheelRef struct {
	URL      string
	SHA256   string // hex digest, no "sha256:" prefix
	Filename string // wheel file name, used for tag parsing
}

// SdistRef is a pinned source distribution, used to build a wheel when no
// pre-built wheel is available.
type SdistRef struct {
	URL      string
	SHA256   string // hex digest, no "sha256:" prefix
	Filename string
}

// Requirement is a single pinned distribution.
type Requirement struct {
	// Name is the project name as written (normalize with NormalizeName for
	// comparisons).
	Name string
	// Version is the exact pinned version (from `==`).
	Version string
	// Hashes are the allowed sha256 hex digests (without the "sha256:" prefix).
	Hashes []string
	// Wheels lists known wheel artifacts for this pin (populated from uv.lock).
	Wheels []WheelRef
	// Sdist, when set, is the source distribution to build a wheel from if no
	// compatible wheel is available.
	Sdist *SdistRef
	// Marker is the PEP 508 environment marker for this requirement (the text
	// after ";"), if any. Evaluated against the target at resolution time.
	Marker string
}

var (
	// pinRE matches "name[extras]==version". Extras are accepted (and ignored
	// for matching, since a resolved lock pins their dependencies separately);
	// environment markers after ";" are also ignored.
	pinRE  = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:\[[^\]]*\])?\s*==\s*([^\s;]+)`)
	hashRE = regexp.MustCompile(`--hash=sha256:([0-9a-fA-F]{64})`)
	normRE = regexp.MustCompile(`[-_.]+`)

	// dependencyOptions reference further requirements rather than configuring
	// the resolver. Silently skipping them could omit dependencies, so we
	// reject them: a lock fed to pymage must be fully inlined and pinned.
	dependencyOptions = []string{"-e", "--editable", "-r", "--requirement", "-c", "--constraint"}
)

// NormalizeName implements PEP 503 name normalization.
func NormalizeName(name string) string {
	return strings.ToLower(normRE.ReplaceAllString(name, "-"))
}

// ParseFile reads and parses a requirements file from disk.
func ParseFile(path string) ([]Requirement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Lock is a parsed lock file (parsed once) that can be resolved per target.
type Lock struct {
	isUV bool
	path string        // path to the lock file on disk
	uv   *uvLockFile   // uv.lock
	reqs []Requirement // requirements format
}

// Options selects the resolution for a uv.lock: a workspace package, extras to
// enable, and the target environment for marker evaluation. They are ignored
// for requirements-format locks (which are already flat and pinned).
type Options struct {
	Package          string
	Extras           []string
	OS, Arch         string
	PyMajor, PyMinor int
}

// Load reads and parses a lock file once. uv.lock is parsed as TOML; everything
// else is treated as a requirements file.
func Load(path string) (*Lock, error) {
	if strings.EqualFold(filepath.Base(path), "uv.lock") {
		lf, err := parseUVLock(path)
		if err != nil {
			return nil, err
		}
		return &Lock{isUV: true, path: path, uv: lf}, nil
	}
	reqs, err := ParseFile(path)
	if err != nil {
		return nil, err
	}
	return &Lock{path: path, reqs: reqs}, nil
}

// Resolve returns the pinned requirements for the given target.
//
// For a uv.lock, resolution (the runtime closure, extras, workspace package,
// and group selection) is delegated to `uv export` — uv is the source of truth,
// so we do not reimplement its resolver. We then evaluate the per-requirement
// environment markers uv emits for the target and attach wheel/sdist URLs from
// the lock. uv is therefore **required** to build from a uv.lock: if it isn't
// available we fail rather than fall back to a divergent resolver. For
// requirements-format locks the list is already flat and pinned; we only
// evaluate any inline markers.
func (l *Lock) Resolve(o Options) ([]Requirement, error) {
	env := o.markerEnv()
	if !l.isUV {
		return filterByMarker(l.reqs, env), nil
	}
	if _, err := exec.LookPath("uv"); err != nil {
		return nil, fmt.Errorf("resolving %s requires uv on PATH (pymage delegates uv.lock resolution to `uv export`); install uv (https://docs.astral.sh/uv/), or export a hashed requirements.txt and build with --lock requirements.txt --find-links", filepath.Base(l.path))
	}
	return uvExportResolve(l.path, l.uv, o, env)
}

// markerEnv builds the marker environment for the target, or nil when no target
// is given (meaning: don't filter on markers).
func (o Options) markerEnv() MarkerEnv {
	if o.PyMajor == 0 {
		return nil
	}
	return NewMarkerEnv(o.OS, o.Arch, o.PyMajor, o.PyMinor)
}

// filterByMarker drops requirements whose marker is false for env. A nil env
// keeps everything.
func filterByMarker(reqs []Requirement, env MarkerEnv) []Requirement {
	out := make([]Requirement, 0, len(reqs))
	for _, r := range reqs {
		if env != nil && !EvalMarker(r.Marker, env) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// DiscoverLock returns the path to a lock file in dir, preferring uv.lock.
func DiscoverLock(dir string) (string, error) {
	for _, name := range []string{"uv.lock", "requirements.lock", "requirements.txt"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no lock file found in %q (expected uv.lock, requirements.lock, or requirements.txt)", dir)
}

// Parse reads pinned requirements from r. Logical lines may span multiple
// physical lines using a trailing backslash, which is how `--hash` entries are
// typically formatted.
func Parse(r io.Reader) ([]Requirement, error) {
	var reqs []Requirement
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var logical strings.Builder
	flush := func() error {
		line := logical.String()
		logical.Reset()
		stripped := stripComment(line)
		if strings.TrimSpace(stripped) == "" {
			return nil
		}
		// Configuration options (e.g. --index-url) aren't pins and are skipped,
		// but options that pull in other requirements must not be silently
		// ignored.
		if isOption(stripped) {
			return optionError(stripped)
		}
		m := pinRE.FindStringSubmatch(stripped)
		if m == nil {
			// We only accept fully-pinned "name==version" requirements. Refuse
			// (rather than silently drop) anything else — unpinned constraints,
			// URL/VCS direct references, `-e` editables — so a lock can never
			// quietly omit a dependency from the image.
			return fmt.Errorf("lock: unsupported requirement line %q (expected name==version)", strings.TrimSpace(stripped))
		}
		req := Requirement{Name: m[1], Version: m[2], Marker: extractMarker(stripped)}
		for _, h := range hashRE.FindAllStringSubmatch(stripped, -1) {
			req.Hashes = append(req.Hashes, strings.ToLower(h[1]))
		}
		reqs = append(reqs, req)
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmed, "\\") {
			logical.WriteString(strings.TrimSuffix(trimmed, "\\"))
			logical.WriteString(" ")
			continue
		}
		logical.WriteString(line)
		if err := flush(); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if logical.Len() > 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return reqs, nil
}

// extractMarker returns the PEP 508 marker on a (comment-stripped) requirement
// line: the text after the first ";" and before any "--hash" entries.
func extractMarker(line string) string {
	i := strings.Index(line, ";")
	if i < 0 {
		return ""
	}
	rest := line[i+1:]
	if h := strings.Index(rest, "--hash"); h >= 0 {
		rest = rest[:h]
	}
	return strings.TrimSpace(rest)
}

func stripComment(s string) string {
	// A '#' starts a comment. Requirements files don't support escaping '#',
	// and no valid pinned/hashed line contains one, so we strip from the first
	// '#' and drop any trailing whitespace it leaves behind.
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, " \t")
}

func isOption(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "-")
}

// optionError returns an error for dependency-bearing options (which we cannot
// honor from a local wheelhouse), and nil for benign configuration options.
func optionError(s string) error {
	t := strings.TrimSpace(s)
	for _, opt := range dependencyOptions {
		if t == opt || strings.HasPrefix(t, opt+" ") || strings.HasPrefix(t, opt+"=") {
			return fmt.Errorf("lock: unsupported option %q; referenced requirements must be inlined and pinned", t)
		}
	}
	return nil
}

// Validate ensures every requirement is pinned and (optionally) hashed.
func Validate(reqs []Requirement, requireHashes bool) error {
	for _, r := range reqs {
		if r.Version == "" {
			return fmt.Errorf("lock: requirement %q is not pinned with ==", r.Name)
		}
		if requireHashes && len(r.Hashes) == 0 && r.Sdist == nil {
			return fmt.Errorf("lock: requirement %q has no --hash entries", r.Name)
		}
	}
	return nil
}
