package sbom

import (
	"bytes"
	"strings"
	"testing"

	"github.com/imjasonh/terraform-playground/py-image-builder/internal/wheelhouse"
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
