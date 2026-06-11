package lock

import (
	"fmt"
	"strconv"
	"strings"
)

// MarkerEnv holds the PEP 508 environment-marker variables for a build target.
type MarkerEnv map[string]string

// NewMarkerEnv derives marker variables from a target os/arch/python version.
func NewMarkerEnv(os, arch string, pyMajor, pyMinor int) MarkerEnv {
	sysPlatform := map[string]string{"linux": "linux", "darwin": "darwin", "windows": "win32"}[os]
	if sysPlatform == "" {
		sysPlatform = os
	}
	platSystem := map[string]string{"linux": "Linux", "darwin": "Darwin", "windows": "Windows"}[os]
	if platSystem == "" {
		platSystem = os
	}
	osName := "posix"
	if os == "windows" {
		osName = "nt"
	}
	machine := map[string]string{
		"amd64": "x86_64", "arm64": "aarch64", "386": "i686",
		"arm": "armv7l", "ppc64le": "ppc64le", "s390x": "s390x",
	}[arch]
	if machine == "" {
		machine = arch
	}
	return MarkerEnv{
		"os_name":                        osName,
		"sys_platform":                   sysPlatform,
		"platform_system":                platSystem,
		"platform_machine":               machine,
		"python_version":                 fmt.Sprintf("%d.%d", pyMajor, pyMinor),
		"python_full_version":            fmt.Sprintf("%d.%d.0", pyMajor, pyMinor),
		"implementation_name":            "cpython",
		"implementation_version":         fmt.Sprintf("%d.%d.0", pyMajor, pyMinor),
		"platform_python_implementation": "CPython",
	}
}

// EvalMarker reports whether a PEP 508 environment marker holds for env. An
// empty marker is always true. If the marker can't be parsed it returns true,
// so an unrecognized marker never silently drops a dependency.
func EvalMarker(marker string, env MarkerEnv) bool {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return true
	}
	toks, err := tokenizeMarker(marker)
	if err != nil {
		return true
	}
	p := &markerParser{toks: toks, env: env}
	v, err := p.parseOr()
	if err != nil || !p.atEnd() {
		return true
	}
	return v
}

type mtokKind int

const (
	mtokString mtokKind = iota
	mtokIdent
	mtokOp
	mtokLParen
	mtokRParen
)

type mtok struct {
	kind mtokKind
	val  string
}

func tokenizeMarker(s string) ([]mtok, error) {
	var toks []mtok
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(s) && s[j] != c {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string")
			}
			toks = append(toks, mtok{mtokString, s[i+1 : j]})
			i = j + 1
		case c == '(':
			toks = append(toks, mtok{mtokLParen, "("})
			i++
		case c == ')':
			toks = append(toks, mtok{mtokRParen, ")"})
			i++
		case c == '=' || c == '!' || c == '<' || c == '>' || c == '~':
			j := i
			for j < len(s) && strings.ContainsRune("=!<>~", rune(s[j])) {
				j++
			}
			toks = append(toks, mtok{mtokOp, s[i:j]})
			i = j
		case isIdentChar(c):
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			toks = append(toks, mtok{mtokIdent, s[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected char %q", c)
		}
	}
	return toks, nil
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

type markerParser struct {
	toks []mtok
	pos  int
	env  MarkerEnv
}

func (p *markerParser) atEnd() bool { return p.pos >= len(p.toks) }

func (p *markerParser) peek() (mtok, bool) {
	if p.atEnd() {
		return mtok{}, false
	}
	return p.toks[p.pos], true
}

func (p *markerParser) parseOr() (bool, error) {
	v, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != mtokIdent || t.val != "or" {
			return v, nil
		}
		p.pos++
		r, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		v = v || r
	}
}

func (p *markerParser) parseAnd() (bool, error) {
	v, err := p.parseAtom()
	if err != nil {
		return false, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != mtokIdent || t.val != "and" {
			return v, nil
		}
		p.pos++
		r, err := p.parseAtom()
		if err != nil {
			return false, err
		}
		v = v && r
	}
}

func (p *markerParser) parseAtom() (bool, error) {
	t, ok := p.peek()
	if !ok {
		return false, fmt.Errorf("unexpected end")
	}
	if t.kind == mtokLParen {
		p.pos++
		v, err := p.parseOr()
		if err != nil {
			return false, err
		}
		end, ok := p.peek()
		if !ok || end.kind != mtokRParen {
			return false, fmt.Errorf("missing )")
		}
		p.pos++
		return v, nil
	}
	return p.parseComparison()
}

func (p *markerParser) parseComparison() (bool, error) {
	l, lver, lextra, err := p.parseValue()
	if err != nil {
		return false, err
	}
	op, err := p.parseOp()
	if err != nil {
		return false, err
	}
	r, rver, rextra, err := p.parseValue()
	if err != nil {
		return false, err
	}
	// `extra` is resolved upstream (we ask uv/pip for the exact extras), so a
	// clause referencing it is neutral here: treat it as satisfied rather than
	// re-deciding extra membership.
	if lextra || rextra {
		return true, nil
	}
	return compareMarker(l, lver, op, r, rver), nil
}

func (p *markerParser) parseOp() (string, error) {
	t, ok := p.peek()
	if !ok {
		return "", fmt.Errorf("expected operator")
	}
	if t.kind == mtokOp {
		p.pos++
		return t.val, nil
	}
	if t.kind == mtokIdent && t.val == "in" {
		p.pos++
		return "in", nil
	}
	if t.kind == mtokIdent && t.val == "not" {
		p.pos++
		n, ok := p.peek()
		if !ok || n.kind != mtokIdent || n.val != "in" {
			return "", fmt.Errorf("expected 'in' after 'not'")
		}
		p.pos++
		return "not in", nil
	}
	return "", fmt.Errorf("expected operator, got %q", t.val)
}

// parseValue returns the value, whether it is a version-valued variable
// (python_version / python_full_version / implementation_version), and whether
// it is the `extra` variable (handled specially by the caller).
func (p *markerParser) parseValue() (val string, isVersion, isExtra bool, err error) {
	t, ok := p.peek()
	if !ok {
		return "", false, false, fmt.Errorf("expected value")
	}
	p.pos++
	switch t.kind {
	case mtokString:
		return t.val, false, false, nil
	case mtokIdent:
		if t.val == "extra" {
			return "", false, true, nil
		}
		v, known := p.env[t.val]
		if !known {
			// Unknown variable: treat as empty, non-version.
			return "", false, false, nil
		}
		isVer := t.val == "python_version" || t.val == "python_full_version" || t.val == "implementation_version"
		return v, isVer, false, nil
	default:
		return "", false, false, fmt.Errorf("unexpected token %q", t.val)
	}
}

func compareMarker(l string, lver bool, op string, r string, rver bool) bool {
	switch op {
	case "in":
		return strings.Contains(r, l)
	case "not in":
		return !strings.Contains(r, l)
	}
	if lver || rver {
		c := compareVersions(l, r)
		switch op {
		case "==", "===":
			return c == 0
		case "!=":
			return c != 0
		case "<":
			return c < 0
		case "<=":
			return c <= 0
		case ">":
			return c > 0
		case ">=", "~=":
			return c >= 0
		}
	}
	switch op {
	case "==", "===":
		return l == r
	case "!=":
		return l != r
	case "<":
		return l < r
	case "<=":
		return l <= r
	case ">":
		return l > r
	case ">=":
		return l >= r
	}
	return false
}

// compareVersions compares dotted numeric versions (e.g. "3.10" vs "3.9").
// Non-numeric components compare lexically; missing components are 0.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var ai, bi string
		if i < len(as) {
			ai = as[i]
		}
		if i < len(bs) {
			bi = bs[i]
		}
		an, aerr := strconv.Atoi(ai)
		bn, berr := strconv.Atoi(bi)
		if aerr == nil && berr == nil {
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
			continue
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	return 0
}
