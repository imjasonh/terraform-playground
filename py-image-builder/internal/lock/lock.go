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
	"regexp"
	"strings"
)

// Requirement is a single pinned distribution.
type Requirement struct {
	// Name is the project name as written (normalize with NormalizeName for
	// comparisons).
	Name string
	// Version is the exact pinned version (from `==`).
	Version string
	// Hashes are the allowed sha256 hex digests (without the "sha256:" prefix).
	Hashes []string
}

var (
	// pinRE matches "name==version" optionally followed by extras/markers we
	// ignore for matching purposes.
	pinRE  = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)\s*==\s*([^\s;]+)`)
	hashRE = regexp.MustCompile(`--hash=sha256:([0-9a-fA-F]{64})`)
	normRE = regexp.MustCompile(`[-_.]+`)
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
	defer f.Close()
	return Parse(f)
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
		// Skip pip options like -r/-c/--index-url that aren't pins.
		if isOption(stripped) {
			return nil
		}
		m := pinRE.FindStringSubmatch(stripped)
		if m == nil {
			// Not a pinned requirement (e.g. a bare option line); ignore.
			return nil
		}
		req := Requirement{Name: m[1], Version: m[2]}
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

func stripComment(s string) string {
	// A '#' starts a comment unless escaped; requirements files don't escape so
	// we treat the first unquoted '#' as a comment start.
	if i := strings.Index(s, " #"); i >= 0 {
		return s[:i]
	}
	if strings.HasPrefix(strings.TrimSpace(s), "#") {
		return ""
	}
	return s
}

func isOption(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "-")
}

// Validate ensures every requirement is pinned and (optionally) hashed.
func Validate(reqs []Requirement, requireHashes bool) error {
	for _, r := range reqs {
		if r.Version == "" {
			return fmt.Errorf("lock: requirement %q is not pinned with ==", r.Name)
		}
		if requireHashes && len(r.Hashes) == 0 {
			return fmt.Errorf("lock: requirement %q has no --hash entries", r.Name)
		}
	}
	return nil
}
