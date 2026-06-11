package sbom

import (
	"bytes"
	"strings"
	"testing"

	"github.com/imjasonh/terraform-playground/pymage/internal/wheelhouse"
)

func TestGenerateDeterministicAndSorted(t *testing.T) {
	wheels := []wheelhouse.ResolvedWheel{
		{Name: "beta", Version: "2.0", SHA256: "bbbb"},
		{Name: "alpha", Version: "1.0", SHA256: "aaaa"},
	}
	a, err := Generate(wheels)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(wheels)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("SBOM not deterministic")
	}
	s := string(a)
	if !strings.Contains(s, "pkg:pypi/alpha@1.0") || !strings.Contains(s, "pkg:pypi/beta@2.0") {
		t.Errorf("missing purls:\n%s", s)
	}
	if strings.Index(s, "alpha") > strings.Index(s, "beta") {
		t.Error("components not sorted (alpha should precede beta)")
	}
}

func TestGenerateNormalizesNames(t *testing.T) {
	// Names are normalized (PEP 503) for canonical PURLs and stable ordering.
	wheels := []wheelhouse.ResolvedWheel{
		{Name: "Werkzeug", Version: "2.3.7", SHA256: "cccc"},
		{Name: "typing_extensions", Version: "4.0", SHA256: "dddd"},
	}
	out, err := Generate(wheels)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "pkg:pypi/werkzeug@2.3.7") {
		t.Errorf("Werkzeug not normalized in PURL:\n%s", s)
	}
	if !strings.Contains(s, "pkg:pypi/typing-extensions@4.0") {
		t.Errorf("typing_extensions not normalized in PURL:\n%s", s)
	}
	if strings.Contains(s, "Werkzeug") || strings.Contains(s, "typing_extensions") {
		t.Errorf("non-normalized names present in SBOM:\n%s", s)
	}
}
