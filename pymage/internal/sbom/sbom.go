// Package sbom emits a minimal, deterministic CycloneDX-style SBOM listing the
// resolved wheels that went into an image. Because the builder already holds
// every wheel's name, version, and sha256, producing an SBOM is essentially
// free.
package sbom

import (
	"encoding/json"
	"sort"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheelhouse"
)

type doc struct {
	BOMFormat   string      `json:"bomFormat"`
	SpecVersion string      `json:"specVersion"`
	Version     int         `json:"version"`
	Components  []component `json:"components"`
}

type component struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
	PURL    string `json:"purl"`
	Hashes  []hash `json:"hashes,omitempty"`
}

type hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

// Generate returns a deterministic CycloneDX JSON document for the wheels.
func Generate(wheels []wheelhouse.ResolvedWheel) ([]byte, error) {
	// Project names are case-insensitive (PEP 503), so sort and build PURLs from
	// the normalized name for stable ordering and canonical pkg:pypi PURLs.
	sorted := append([]wheelhouse.ResolvedWheel(nil), wheels...)
	sort.Slice(sorted, func(i, j int) bool {
		ni, nj := lock.NormalizeName(sorted[i].Name), lock.NormalizeName(sorted[j].Name)
		if ni != nj {
			return ni < nj
		}
		return sorted[i].Version < sorted[j].Version
	})

	d := doc{BOMFormat: "CycloneDX", SpecVersion: "1.5", Version: 1}
	for _, w := range sorted {
		norm := lock.NormalizeName(w.Name)
		d.Components = append(d.Components, component{
			Type:    "library",
			Name:    norm,
			Version: w.Version,
			PURL:    "pkg:pypi/" + norm + "@" + w.Version,
			Hashes:  []hash{{Alg: "SHA-256", Content: w.SHA256}},
		})
	}
	return json.MarshalIndent(d, "", "  ")
}
