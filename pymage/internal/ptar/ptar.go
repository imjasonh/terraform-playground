// Package ptar builds deterministic (reproducible) tar archives and turns them
// into OCI layers.
//
// Reproducibility is the load-bearing property of pymage: identical
// inputs must yield byte-identical layers so that their content-addressed
// digests match and registries can dedupe/skip/mount them. To guarantee that,
// every tar we emit:
//
//   - sorts entries by path,
//   - synthesizes parent directory entries deterministically,
//   - zeroes mtime (a fixed epoch), uid/gid, and uname/gname,
//   - uses a fixed permission scheme (0644 files, 0755 dirs, 0755 for
//     executables),
//   - never emits device, xattr, or PAX time records.
package ptar

import (
	"archive/tar"
	"bytes"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// Epoch is the fixed modification time stamped on every tar entry. We use the
// Unix epoch rather than time.Now so builds are reproducible.
const Epoch = 0

func unixEpoch() time.Time { return time.Unix(Epoch, 0).UTC() }

// File is a single regular file to include in a tar archive. Directories are
// synthesized automatically from the set of file paths.
type File struct {
	// Path is the slash-separated path within the image, without a leading
	// slash (e.g. "app/.venv/lib/python3.12/site-packages/foo/__init__.py").
	Path string
	// Data is the file contents.
	Data []byte
	// Executable marks the file mode as 0755 instead of 0644.
	Executable bool
}

const (
	fileMode = 0o644
	execMode = 0o755
	dirMode  = 0o755
)

// WriteTar writes a deterministic, uncompressed tar stream of files to w.
//
// The same files (regardless of input order) always produce byte-identical
// output.
func WriteTar(w io.Writer, files []File) error {
	tw := tar.NewWriter(w)

	// Collect the set of directories implied by the files, plus the files
	// themselves, then emit everything in a single sorted pass so the order is
	// stable and parents always precede children.
	dirs := map[string]struct{}{}
	for _, f := range files {
		clean := path.Clean(strings.TrimPrefix(f.Path, "/"))
		dir := path.Dir(clean)
		for dir != "." && dir != "/" && dir != "" {
			dirs[dir] = struct{}{}
			dir = path.Dir(dir)
		}
	}

	type entry struct {
		name string
		dir  bool
		file File
	}
	entries := make([]entry, 0, len(files)+len(dirs))
	for d := range dirs {
		entries = append(entries, entry{name: d + "/", dir: true})
	}
	seen := map[string]bool{}
	for _, f := range files {
		clean := path.Clean(strings.TrimPrefix(f.Path, "/"))
		if seen[clean] {
			continue
		}
		seen[clean] = true
		entries = append(entries, entry{name: clean, file: f})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	for _, e := range entries {
		if e.dir {
			if err := tw.WriteHeader(&tar.Header{
				Name:     e.name,
				Typeflag: tar.TypeDir,
				Mode:     dirMode,
				ModTime:  unixEpoch(),
				Format:   tar.FormatPAX,
			}); err != nil {
				return err
			}
			continue
		}
		mode := int64(fileMode)
		if e.file.Executable {
			mode = execMode
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Typeflag: tar.TypeReg,
			Mode:     mode,
			Size:     int64(len(e.file.Data)),
			ModTime:  unixEpoch(),
			Format:   tar.FormatPAX,
		}); err != nil {
			return err
		}
		if _, err := tw.Write(e.file.Data); err != nil {
			return err
		}
	}
	return tw.Close()
}

// TarBytes returns the deterministic tar stream for files.
func TarBytes(files []File) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteTar(&buf, files); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Layer builds a reproducible OCI layer from files.
//
// The returned layer compresses deterministically (go-containerregistry uses
// stdlib gzip at a fixed level with a zeroed header), so its digest is stable
// across builds.
func Layer(files []File) (v1.Layer, error) {
	raw, err := TarBytes(files)
	if err != nil {
		return nil, err
	}
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(raw)), nil
	})
}
