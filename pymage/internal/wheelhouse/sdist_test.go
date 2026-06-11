package wheelhouse

import (
	"context"
	"strings"
	"testing"

	"github.com/imjasonh/terraform-playground/pymage/internal/lock"
	"github.com/imjasonh/terraform-playground/pymage/internal/wheel"
)

// pymage does not build sdists: a requirement available only as a source
// distribution must error with guidance to pre-build a wheel and use
// --find-links, rather than executing the dependency's build code.
func TestSdistOnlyErrors(t *testing.T) {
	req := lock.Requirement{
		Name:    "timeout-decorator",
		Version: "0.5.0",
		Sdist:   &lock.SdistRef{URL: "https://example/timeout_decorator-0.5.0.tar.gz", SHA256: "deadbeef", Filename: "timeout_decorator-0.5.0.tar.gz"},
	}
	target := wheel.Target{OS: "linux", Arch: "amd64", PyMajor: 3, PyMinor: 12}

	_, err := ResolveContext(context.Background(), []lock.Requirement{req}, nil, target, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for an sdist-only requirement")
	}
	for _, want := range []string{"source distribution", "--find-links", "timeout-decorator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}
