package lock

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// uvExportResolve delegates resolution to `uv export`, which produces the
// project's universal runtime requirement set (closure, extras, groups, and
// workspace selection all handled by uv) with per-requirement markers and
// hashes. We then evaluate the markers for the target and attach wheel/sdist
// URLs from the parsed uv.lock so the downloader can fetch them.
//
// `uv export --frozen` reads the existing lock; it does not re-resolve, run
// build code, or hit the network.
func uvExportResolve(lockPath string, lf *uvLockFile, o Options, env MarkerEnv) ([]Requirement, error) {
	args := []string{
		"export", "--frozen", "--no-dev",
		"--no-emit-workspace", // omit the project/workspace members themselves
		"--no-annotate", "--format", "requirements-txt",
	}
	if o.Package != "" {
		args = append(args, "--package", o.Package)
	}
	for _, e := range o.Extras {
		args = append(args, "--extra", e)
	}

	cmd := exec.Command("uv", args...)
	cmd.Dir = filepath.Dir(lockPath)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("uv export: %w: %s", err, strings.TrimSpace(errb.String()))
	}

	pins, err := Parse(&out)
	if err != nil {
		return nil, fmt.Errorf("parse uv export output: %w", err)
	}
	return mergeExportPins(pins, lf, env)
}

// mergeExportPins filters the exported pins by their markers and attaches
// wheel/sdist artifacts from the lock. Split out from uvExportResolve so it can
// be tested without invoking uv.
func mergeExportPins(pins []Requirement, lf *uvLockFile, env MarkerEnv) ([]Requirement, error) {
	byName := make(map[string]*uvPackage, len(lf.Package))
	for i := range lf.Package {
		byName[NormalizeName(lf.Package[i].Name)] = &lf.Package[i]
	}

	out := make([]Requirement, 0, len(pins))
	for _, r := range pins {
		if env != nil && !EvalMarker(r.Marker, env) {
			continue
		}
		if p := byName[NormalizeName(r.Name)]; p != nil {
			if err := attachArtifacts(&r, p); err != nil {
				return nil, err
			}
		}
		// If the package isn't in the lock (shouldn't happen with --frozen) we
		// keep the pin with its export hashes; the wheelhouse will report a
		// clear "no wheel" error rather than silently dropping it.
		out = append(out, r)
	}
	return out, nil
}

// attachArtifacts copies the wheel URLs and sdist reference from a uv.lock
// package onto a requirement resolved via uv export. Hashes already come from
// the export output.
func attachArtifacts(r *Requirement, p *uvPackage) error {
	for _, w := range p.Wheels {
		if w.URL == "" {
			continue
		}
		sum, err := parseUVHash(w.Hash)
		if err != nil {
			return fmt.Errorf("uv.lock: %s==%s: %w", p.Name, p.Version, err)
		}
		r.Wheels = append(r.Wheels, WheelRef{URL: w.URL, SHA256: sum, Filename: filepath.Base(w.URL)})
	}
	if p.Sdist.URL != "" {
		sum, err := parseUVHash(p.Sdist.Hash)
		if err != nil {
			return fmt.Errorf("uv.lock: %s==%s sdist: %w", p.Name, p.Version, err)
		}
		r.Sdist = &SdistRef{URL: p.Sdist.URL, SHA256: sum, Filename: filepath.Base(p.Sdist.URL)}
	}
	return nil
}
