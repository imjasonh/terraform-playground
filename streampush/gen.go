package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
)

// genReader produces exactly `remaining` bytes on demand, filling each Read
// buffer with `fill` and incrementing `counter` atomically. It never allocates
// per Read and never buffers content, so it can back an arbitrarily large layer
// with constant memory.
type genReader struct {
	remaining int64
	fill      func([]byte)
	counter   *int64
}

func (g *genReader) Read(p []byte) (int, error) {
	if g.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > g.remaining {
		n = int(g.remaining)
	}
	g.fill(p[:n])
	g.remaining -= int64(n)
	atomic.AddInt64(g.counter, int64(n))
	return n, nil
}

// Close implements io.Closer so genReader satisfies io.ReadCloser, which is
// what stream.NewLayer expects.
func (g *genReader) Close() error { return nil }

// fillFunc returns a buffer-filling function for the requested data mode.
//
//   - "zero":   write zero bytes. Trivial CPU; gzip crushes it (use with
//     --compression none to still put real bytes on the wire).
//   - "random": fill with a fast, allocation-free xorshift PRNG. Incompressible,
//     so the on-wire size tracks the generated size even under gzip, at the cost
//     of more CPU.
func fillFunc(mode string) (func([]byte), error) {
	switch strings.ToLower(mode) {
	case "zero", "zeros", "0":
		return func(b []byte) { clear(b) }, nil
	case "random", "rand":
		// One state per returned closure; Reads are sequential within a layer.
		state := uint64(0x9e3779b97f4a7c15)
		return func(b []byte) {
			i := 0
			for ; i+8 <= len(b); i += 8 {
				state ^= state << 13
				state ^= state >> 7
				state ^= state << 17
				b[i+0] = byte(state)
				b[i+1] = byte(state >> 8)
				b[i+2] = byte(state >> 16)
				b[i+3] = byte(state >> 24)
				b[i+4] = byte(state >> 32)
				b[i+5] = byte(state >> 40)
				b[i+6] = byte(state >> 48)
				b[i+7] = byte(state >> 56)
			}
			if i < len(b) {
				state ^= state << 13
				state ^= state >> 7
				state ^= state << 17
				for j := 0; i < len(b); i, j = i+1, j+1 {
					b[i] = byte(state >> (8 * j))
				}
			}
		}, nil
	default:
		return nil, fmt.Errorf("unknown data mode %q (want zero or random)", mode)
	}
}

// parseSize parses human-friendly sizes: a bare integer (bytes), or a number
// with a decimal (KB/MB/GB/TB) or binary (KiB/MiB/GiB/TiB) unit suffix.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	upper := strings.ToUpper(s)

	type unit struct {
		suffix string
		mult   int64
	}
	// Order matters: check longer/binary suffixes before shorter decimal ones.
	units := []unit{
		{"TIB", 1 << 40}, {"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3},
		{"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10},
		{"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(upper, u.suffix) {
			num := strings.TrimSpace(upper[:len(upper)-len(u.suffix)])
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, fmt.Errorf("parse %q: %w", s, err)
			}
			return int64(f * float64(u.mult)), nil
		}
	}
	n, err := strconv.ParseInt(upper, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return n, nil
}

// humanBytes formats a byte count using binary (IEC) units.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
