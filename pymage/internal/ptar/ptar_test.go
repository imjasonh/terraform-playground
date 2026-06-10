package ptar

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
)

func TestWriteTarDeterministicAcrossOrder(t *testing.T) {
	a := []File{
		{Path: "b/y.txt", Data: []byte("y")},
		{Path: "a/x.txt", Data: []byte("x")},
		{Path: "a/z.txt", Data: []byte("z")},
	}
	b := []File{
		{Path: "a/z.txt", Data: []byte("z")},
		{Path: "a/x.txt", Data: []byte("x")},
		{Path: "b/y.txt", Data: []byte("y")},
	}
	ba, err := TarBytes(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := TarBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ba, bb) {
		t.Fatal("tar bytes differ for the same files in different input order")
	}
}

func TestTarSynthesizesParentDirsAndSorts(t *testing.T) {
	raw, err := TarBytes([]File{
		{Path: "app/pkg/mod.py", Data: []byte("x")},
		{Path: "app/main.py", Data: []byte("y"), Executable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	var names []string
	modes := map[string]int64{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
		modes[h.Name] = h.Mode
		if !h.ModTime.Equal(unixEpoch()) {
			t.Errorf("entry %q has non-epoch mtime %v", h.Name, h.ModTime)
		}
		if h.Uid != 0 || h.Gid != 0 {
			t.Errorf("entry %q has non-zero uid/gid", h.Name)
		}
	}
	want := []string{"app/", "app/main.py", "app/pkg/", "app/pkg/mod.py"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
	if modes["app/main.py"] != execMode {
		t.Errorf("executable mode = %o, want %o", modes["app/main.py"], execMode)
	}
	if modes["app/pkg/mod.py"] != fileMode {
		t.Errorf("file mode = %o, want %o", modes["app/pkg/mod.py"], fileMode)
	}
}

func TestLayerStableDigest(t *testing.T) {
	files := []File{
		{Path: "site-packages/foo/__init__.py", Data: []byte("print('hi')\n")},
		{Path: "site-packages/foo/util.py", Data: []byte("X = 1\n")},
	}
	l1, err := Layer(files)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := Layer(files)
	if err != nil {
		t.Fatal(err)
	}
	d1, err := l1.Digest()
	if err != nil {
		t.Fatal(err)
	}
	d2, err := l2.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("layer digests differ across builds: %s vs %s", d1, d2)
	}
	diff1, _ := l1.DiffID()
	diff2, _ := l2.DiffID()
	if diff1 != diff2 {
		t.Fatalf("layer diffIDs differ across builds: %s vs %s", diff1, diff2)
	}
}
