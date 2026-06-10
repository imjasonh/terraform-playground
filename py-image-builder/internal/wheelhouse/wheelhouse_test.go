package wheelhouse

import (
	"testing"

	"github.com/imjasonh/terraform-playground/py-image-builder/internal/lock"
	"github.com/imjasonh/terraform-playground/py-image-builder/internal/testwheel"
)

func TestResolveSortedAndVerified(t *testing.T) {
	dir := t.TempDir()
	_, shaB := testwheel.Write(t, dir, testwheel.Spec{Name: "beta", Version: "2.0", Modules: map[string]string{"beta.py": "x=1\n"}})
	_, shaA := testwheel.Write(t, dir, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha.py": "x=1\n"}})

	reqs := []lock.Requirement{
		{Name: "Beta", Version: "2.0", Hashes: []string{shaB}},
		{Name: "alpha", Version: "1.0", Hashes: []string{shaA}},
	}
	got, err := Resolve(reqs, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d wheels", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "Beta" {
		t.Fatalf("not sorted by normalized name: %+v", got)
	}
	if got[0].SHA256 != shaA {
		t.Errorf("alpha sha mismatch")
	}
}

func TestResolveHashMismatch(t *testing.T) {
	dir := t.TempDir()
	testwheel.Write(t, dir, testwheel.Spec{Name: "alpha", Version: "1.0", Modules: map[string]string{"alpha.py": "x=1\n"}})
	reqs := []lock.Requirement{{Name: "alpha", Version: "1.0", Hashes: []string{"deadbeef"}}}
	if _, err := Resolve(reqs, []string{dir}); err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

func TestResolveMissing(t *testing.T) {
	dir := t.TempDir()
	reqs := []lock.Requirement{{Name: "ghost", Version: "9.9"}}
	if _, err := Resolve(reqs, []string{dir}); err == nil {
		t.Fatal("expected missing-wheel error")
	}
}
