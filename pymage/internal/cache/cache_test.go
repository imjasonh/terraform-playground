package cache

import (
	"testing"

	"github.com/imjasonh/terraform-playground/pymage/internal/ptar"
)

func TestPutGetRoundTrip(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	layer, err := ptar.Layer([]ptar.File{{Path: "site/foo.py", Data: []byte("X=1\n")}})
	if err != nil {
		t.Fatal(err)
	}
	want, err := layer.Digest()
	if err != nil {
		t.Fatal(err)
	}

	const key = "wheel|1|abc|/app/.venv|python3.12"
	if _, ok := c.Get(key); ok {
		t.Fatal("expected a miss before Put")
	}
	if err := c.Put(key, layer); err != nil {
		t.Fatal(err)
	}

	cached, ok := c.Get(key)
	if !ok {
		t.Fatal("expected a hit after Put")
	}
	got, err := cached.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cached layer digest %s != original %s", got, want)
	}
}
