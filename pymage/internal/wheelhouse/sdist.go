package wheelhouse

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/pelletier/go-toml/v2"
)

// repackSdist turns a *pure-Python* source distribution into a wheel WITHOUT
// executing any of the package's build code — no setup.py, no PEP 517 hook, no
// RCE vector. It reads only the static artifacts a compliant sdist already
// carries (PKG-INFO for metadata; *.egg-info/{top_level,entry_points}.txt or the
// declarative layout for the file set) and deterministically repacks the
// importable files into a py3-none-any wheel.
//
// It is deliberately conservative: anything that would actually require a build
// — native sources, a compiling build backend, or external data_files we might
// miss — is rejected (the caller then surfaces the --find-links guidance), so we
// never ship a silently-wrong wheel.
func repackSdist(ctx context.Context, req lock.Requirement, cache *wheelCache) (ResolvedWheel, error) {
	if cache == nil {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: repacking %s==%s from sdist needs a cache dir (don't combine --repack-sdists with --no-cache)", req.Name, req.Version)
	}
	key := "sdist-" + req.Sdist.SHA256 // pure wheel: platform-independent
	if p, ok := cache.getBuilt(key); ok {
		sum, err := fileSHA256(p)
		if err != nil {
			return ResolvedWheel{}, err
		}
		return ResolvedWheel{Name: req.Name, Version: req.Version, Path: p, SHA256: sum}, nil
	}

	gz, err := download(ctx, req.Sdist.URL, req.Sdist.SHA256)
	if err != nil {
		return ResolvedWheel{}, fmt.Errorf("wheelhouse: %s==%s sdist: %w", req.Name, req.Version, err)
	}
	name, wheelBytes, err := synthWheelFromSdist(gz, req)
	if err != nil {
		return ResolvedWheel{}, err
	}

	dst := cache.builtPath(key)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return ResolvedWheel{}, err
	}
	if err := os.WriteFile(dst, wheelBytes, 0o644); err != nil {
		return ResolvedWheel{}, err
	}
	sum := sha256.Sum256(wheelBytes)
	_ = name
	return ResolvedWheel{Name: req.Name, Version: req.Version, Path: dst, SHA256: hex.EncodeToString(sum[:])}, nil
}

func (c *wheelCache) builtPath(key string) string { return filepath.Join(c.dir, "built", key+".whl") }

func (c *wheelCache) getBuilt(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	if _, err := os.Stat(c.builtPath(key)); err == nil {
		return c.builtPath(key), true
	}
	return "", false
}

// nativeExts are source files that imply a compile step (i.e. not pure Python).
var nativeExts = map[string]bool{
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".cxx": true, ".hpp": true,
	".pyx": true, ".pxd": true, ".pxi": true, ".cu": true, ".rs": true, ".go": true,
	".f": true, ".f90": true, ".m": true, ".mm": true, ".s": true,
}

// compilingBackends in [build-system].requires imply a build that produces
// native artifacts; such sdists can't be safely repacked.
var compilingBackends = []string{
	"cython", "setuptools-rust", "setuptools_rust", "maturin", "scikit-build",
	"scikit_build", "meson", "pybind11", "nanobind", "cffi", "numpy",
}

// synthWheelFromSdist builds the wheel bytes from an sdist's gzip-compressed tar.
// Pure (no network) for testability.
func synthWheelFromSdist(gzData []byte, req lock.Requirement) (string, []byte, error) {
	files, err := readTarGz(gzData)
	if err != nil {
		return "", nil, fmt.Errorf("wheelhouse: %s==%s: read sdist: %w", req.Name, req.Version, err)
	}
	files = stripRoot(files)

	reject := func(why string) (string, []byte, error) {
		return "", nil, fmt.Errorf("wheelhouse: %s==%s cannot be repacked without building (%s); pre-build a wheel and pass --find-links instead", req.Name, req.Version, why)
	}

	// Metadata comes from the sdist's static PKG-INFO (resolved at sdist time,
	// so even setup.py/dynamic projects expose final values here).
	pkgInfo, ok := files["PKG-INFO"]
	if !ok {
		return reject("no PKG-INFO in sdist")
	}
	hdr := parseRFC822(pkgInfo)
	if lock.NormalizeName(hdr["name"]) != lock.NormalizeName(req.Name) {
		return reject(fmt.Sprintf("PKG-INFO Name %q != %q", hdr["name"], req.Name))
	}
	if v := strings.TrimSpace(hdr["version"]); v != req.Version {
		return reject(fmt.Sprintf("PKG-INFO Version %q != %q", v, req.Version))
	}

	// Guard: a compiling build backend means native output.
	if py, ok := files["pyproject.toml"]; ok && backendCompiles(py) {
		return reject("build-system requires a compiling backend")
	}
	// Guard: external data files we might not capture.
	for _, f := range []string{"setup.py", "setup.cfg", "pyproject.toml"} {
		if b, ok := files[f]; ok && bytes.Contains(bytes.ToLower(b), []byte("data_files")) {
			return reject("declares data_files")
		}
	}

	tops, base, err := topLevels(files, req)
	if err != nil {
		return "", nil, fmt.Errorf("wheelhouse: %s==%s: %w; pre-build a wheel and pass --find-links instead", req.Name, req.Version, err)
	}

	// Collect the importable payload, rejecting any native source we'd be asked
	// to ship as-is (it would need compilation).
	payload := map[string][]byte{}
	for _, top := range tops {
		for rel, data := range files {
			var dest string
			switch {
			case rel == base+top+".py":
				dest = top + ".py"
			case strings.HasPrefix(rel, base+top+"/"):
				dest = strings.TrimPrefix(rel, base)
			default:
				continue
			}
			b := path.Base(dest)
			if b == "" || strings.Contains(dest, "__pycache__") || strings.HasSuffix(dest, ".pyc") || strings.HasSuffix(dest, ".pyo") {
				continue
			}
			if nativeExts[strings.ToLower(filepath.Ext(dest))] {
				return reject("contains native sources (" + dest + ")")
			}
			payload[dest] = data
		}
	}
	if len(payload) == 0 {
		return reject("no importable files found for " + strings.Join(tops, ","))
	}

	// dist-info
	distName := wheelDistName(req.Name)
	di := fmt.Sprintf("%s-%s.dist-info", distName, req.Version)
	payload[di+"/METADATA"] = pkgInfo
	payload[di+"/WHEEL"] = []byte("Wheel-Version: 1.0\nGenerator: pymage-repack\nRoot-Is-Purelib: true\nTag: py3-none-any\n")
	if ep := entryPoints(files); ep != "" {
		payload[di+"/entry_points.txt"] = []byte(ep)
	}
	payload[di+"/RECORD"] = record(payload, di)

	wheelName := fmt.Sprintf("%s-%s-py3-none-any.whl", distName, req.Version)
	zb, err := buildWheelZip(payload)
	if err != nil {
		return "", nil, err
	}
	return wheelName, zb, nil
}

// topLevels determines the importable top-level names and the path prefix they
// live under in the sdist ("" or "src/").
func topLevels(files map[string][]byte, req lock.Requirement) (tops []string, base string, err error) {
	// 1. Prefer an egg-info top_level.txt (present in setuptools sdists, incl.
	//    setup.py-based ones).
	for rel, data := range files {
		if strings.HasSuffix(rel, ".egg-info/top_level.txt") {
			base = rel[:strings.Index(rel, ".egg-info/top_level.txt")]
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[:i+1] // e.g. "src/"
			} else {
				base = ""
			}
			for _, ln := range strings.Split(string(data), "\n") {
				if t := strings.TrimSpace(ln); t != "" {
					tops = append(tops, t)
				}
			}
			if len(tops) > 0 {
				sort.Strings(tops)
				return tops, base, nil
			}
		}
	}

	// 2. src/ layout: importable packages/modules directly under src/.
	if hasDir(files, "src/") {
		tops = topsUnder(files, "src/")
		if len(tops) > 0 {
			return tops, "src/", nil
		}
	}

	// 3. Flat layout: a package or module matching the (underscored) name.
	cand := strings.ReplaceAll(lock.NormalizeName(req.Name), "-", "_")
	if _, ok := files[cand+"/__init__.py"]; ok {
		return []string{cand}, "", nil
	}
	if _, ok := files[cand+".py"]; ok {
		return []string{cand}, "", nil
	}
	return nil, "", fmt.Errorf("could not determine importable top-level package (unusual layout)")
}

func topsUnder(files map[string][]byte, base string) []string {
	set := map[string]bool{}
	for rel := range files {
		if !strings.HasPrefix(rel, base) {
			continue
		}
		r := strings.TrimPrefix(rel, base)
		seg := r
		if i := strings.Index(r, "/"); i >= 0 {
			seg = r[:i]
		}
		if strings.HasSuffix(seg, ".egg-info") || strings.HasSuffix(seg, ".dist-info") {
			continue
		}
		// package (has __init__.py) or a top-level module file
		if _, ok := files[base+seg+"/__init__.py"]; ok {
			set[seg] = true
		} else if strings.HasSuffix(r, ".py") && !strings.Contains(r, "/") {
			set[strings.TrimSuffix(seg, ".py")] = true
		}
	}
	var out []string
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func hasDir(files map[string][]byte, prefix string) bool {
	for rel := range files {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func backendCompiles(pyproject []byte) bool {
	var doc struct {
		BuildSystem struct {
			Requires []string `toml:"requires"`
		} `toml:"build-system"`
	}
	if err := toml.Unmarshal(pyproject, &doc); err != nil {
		return false
	}
	joined := strings.ToLower(strings.Join(doc.BuildSystem.Requires, " "))
	for _, b := range compilingBackends {
		if strings.Contains(joined, b) {
			return true
		}
	}
	return false
}

// entryPoints returns entry_points.txt content from egg-info if present, else
// synthesizes it from a declarative pyproject [project.scripts]/gui-scripts/
// entry-points. Returns "" when there are none.
func entryPoints(files map[string][]byte) string {
	for rel, data := range files {
		if strings.HasSuffix(rel, ".egg-info/entry_points.txt") {
			return string(data)
		}
	}
	py, ok := files["pyproject.toml"]
	if !ok {
		return ""
	}
	var doc struct {
		Project struct {
			Scripts     map[string]string            `toml:"scripts"`
			GUIScripts  map[string]string            `toml:"gui-scripts"`
			EntryPoints map[string]map[string]string `toml:"entry-points"`
		} `toml:"project"`
	}
	if err := toml.Unmarshal(py, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	writeGroup := func(group string, m map[string]string) {
		if len(m) == 0 {
			return
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "[%s]\n", group)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", k, m[k])
		}
		b.WriteString("\n")
	}
	writeGroup("console_scripts", doc.Project.Scripts)
	writeGroup("gui_scripts", doc.Project.GUIScripts)
	groups := make([]string, 0, len(doc.Project.EntryPoints))
	for g := range doc.Project.EntryPoints {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		writeGroup(g, doc.Project.EntryPoints[g])
	}
	return strings.TrimRight(b.String(), "\n")
}

func wheelDistName(name string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' {
			b.WriteRune(r)
			prevUnderscore = false
		} else if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func record(payload map[string][]byte, di string) []byte {
	names := make([]string, 0, len(payload))
	for n := range payload {
		if n == di+"/RECORD" {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		sum := sha256.Sum256(payload[n])
		h := base64.RawURLEncoding.EncodeToString(sum[:])
		fmt.Fprintf(&b, "%s,sha256=%s,%d\n", n, h, len(payload[n]))
	}
	fmt.Fprintf(&b, "%s/RECORD,,\n", di)
	return []byte(b.String())
}

// buildWheelZip writes a deterministic wheel zip (sorted entries, fixed mtime).
func buildWheelZip(payload map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(payload))
	for n := range payload {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	epoch := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: epoch})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(payload[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readTarGz(gzData []byte) (map[string][]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(gzData))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !h.FileInfo().Mode().IsRegular() {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, 64<<20))
		if err != nil {
			return nil, err
		}
		out[path.Clean(h.Name)] = data
	}
	return out, nil
}

// stripRoot removes the single leading "<name>-<version>/" directory that
// sdists wrap their contents in.
func stripRoot(files map[string][]byte) map[string][]byte {
	root := ""
	for rel := range files {
		i := strings.Index(rel, "/")
		if i < 0 {
			return files // no common root
		}
		r := rel[:i]
		if root == "" {
			root = r
		} else if root != r {
			return files
		}
	}
	if root == "" {
		return files
	}
	out := make(map[string][]byte, len(files))
	for rel, data := range files {
		out[strings.TrimPrefix(rel, root+"/")] = data
	}
	return out
}

func parseRFC822(b []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			break // headers end at the first blank line (body follows)
		}
		if i := strings.Index(line, ":"); i > 0 {
			k := strings.ToLower(strings.TrimSpace(line[:i]))
			if _, dup := m[k]; !dup {
				m[k] = strings.TrimSpace(line[i+1:])
			}
		}
	}
	return m
}

func download(ctx context.Context, url, wantSum string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, err
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); !strings.EqualFold(got, wantSum) {
		return nil, fmt.Errorf("sdist hash %s != expected %s", got, wantSum)
	}
	return data, nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
