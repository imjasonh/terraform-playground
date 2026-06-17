// Command streampush pushes a container image whose single layer is generated
// on the fly as it uploads. The layer content is never buffered: bytes are
// produced by a generator, gzipped by go-containerregistry's stream.Layer, and
// streamed to the registry in a single chunked PATCH. The layer's digest and
// diffID are only known once the upload completes.
//
// This lets you push, e.g., a 200GB layer using a tiny, constant amount of
// memory on both the client and (with the default streaming registry) the
// server.
package main

import (
	"compress/gzip"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
)

func main() {
	var (
		sizeStr     = flag.String("size", "200GB", "uncompressed layer size to generate (e.g. 200GB, 512MiB, 1073741824)")
		repo        = flag.String("repo", "streampush/big", "repository name to push to")
		tag         = flag.String("tag", "latest", "tag to push")
		registryArg = flag.String("registry", "stream", `target registry: "stream" (built-in non-buffering sink), "pkg" (go-containerregistry in-memory registry; buffers!), or an explicit host[:port] to push to a real registry`)
		insecure    = flag.Bool("insecure", false, "use http (and skip TLS) when pushing to an explicit --registry host")
		dataMode    = flag.String("data", "zero", `layer byte pattern: "zero" (fast, compressible) or "random" (incompressible, CPU heavy)`)
		compArg     = flag.String("compression", "none", `gzip level: "none", "speed", "default", "best", or 0-9`)
		smallLimit  = flag.String("retain-below", "32MiB", "sink registry retains blob content below this size (so config/manifest stay pullable)")
		verbose     = flag.Bool("verbose", false, "verbose registry + push logging")
	)
	flag.Parse()

	size, err := parseSize(*sizeStr)
	if err != nil {
		fatalf("invalid --size: %v", err)
	}
	retainBelow, err := parseSize(*smallLimit)
	if err != nil {
		fatalf("invalid --retain-below: %v", err)
	}
	level, err := parseCompression(*compArg)
	if err != nil {
		fatalf("invalid --compression: %v", err)
	}
	fill, err := fillFunc(*dataMode)
	if err != nil {
		fatalf("invalid --data: %v", err)
	}

	ctx := context.Background()

	// Resolve the target registry host, standing up an in-process server if needed.
	host, nameOpts, cleanup := resolveRegistry(*registryArg, *insecure, retainBelow, *verbose, size)
	defer cleanup()

	ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", host, *repo, *tag), nameOpts...)
	if err != nil {
		fatalf("parse reference: %v", err)
	}

	// The generator: produces `size` bytes on demand, counting as it goes.
	var generated int64
	gen := &genReader{remaining: size, fill: fill, counter: &generated}

	// stream.Layer reads the generator exactly once, gzipping on the fly and
	// computing the compressed digest, uncompressed diffID, and size during the
	// single streaming pass. None of it is buffered.
	layer := stream.NewLayer(gen, stream.WithCompressionLevel(level))

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		fatalf("append layer: %v", err)
	}

	fmt.Printf("streampush: pushing %s\n", ref)
	fmt.Printf("  layer size (uncompressed): %s (%d bytes)\n", humanBytes(size), size)
	fmt.Printf("  data=%s compression=%s registry=%s\n", *dataMode, *compArg, *registryArg)
	fmt.Println("  content is generated as it uploads; digest is finalized at the end.")
	fmt.Println()

	// remote.WithProgress reports compressed bytes accepted by the registry.
	progressCh := make(chan v1.Update, 64)
	var pushed int64
	go func() {
		for u := range progressCh {
			atomic.StoreInt64(&pushed, u.Complete)
		}
	}()

	start := time.Now()
	stopReport := make(chan struct{})
	var peakHeap int64
	go reportProgress(&generated, &pushed, &peakHeap, start, stopReport)

	writeOpts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithProgress(progressCh),
	}

	err = remote.Write(ref, img, writeOpts...)
	close(stopReport)
	if err != nil {
		fatalf("push failed: %v", err)
	}

	// Ensure we have at least one heap sample even for very fast pushes.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if h := int64(ms.HeapAlloc); h > atomic.LoadInt64(&peakHeap) {
		atomic.StoreInt64(&peakHeap, h)
	}

	// All values are now computed because the stream was fully consumed.
	digest, _ := layer.Digest()
	diffID, _ := layer.DiffID()
	compressedSize, _ := layer.Size()
	manifestDigest, _ := img.Digest()
	elapsed := time.Since(start)

	fmt.Println()
	fmt.Println("push complete.")
	fmt.Printf("  generated (uncompressed): %s\n", humanBytes(atomic.LoadInt64(&generated)))
	fmt.Printf("  uploaded  (compressed):   %s\n", humanBytes(compressedSize))
	fmt.Printf("  layer digest  (blob):     %s\n", digest)
	fmt.Printf("  layer diffID  (tar):      %s\n", diffID)
	fmt.Printf("  manifest:                 %s@%s\n", ref.Context().Name(), manifestDigest)
	fmt.Printf("  elapsed: %s (%s/s generated)\n", elapsed.Round(time.Millisecond),
		humanBytes(int64(float64(atomic.LoadInt64(&generated))/elapsed.Seconds())))
	fmt.Printf("  peak Go heap during push: %s (constant regardless of layer size)\n",
		humanBytes(atomic.LoadInt64(&peakHeap)))
}

// resolveRegistry returns the registry host to target, name parse options, and
// a cleanup function. For "stream" and "pkg" it stands up an in-process httptest
// server; for an explicit host it pushes to that registry directly.
func resolveRegistry(arg string, insecure bool, retainBelow int64, verbose bool, size int64) (string, []name.Option, func()) {
	switch arg {
	case "stream":
		srv := httptest.NewServer(newSinkRegistry(retainBelow, verbose))
		host := strings.TrimPrefix(srv.URL, "http://")
		return host, []name.Option{name.Insecure}, srv.Close

	case "pkg":
		if size > 1<<30 {
			fmt.Fprintf(os.Stderr,
				"WARNING: --registry pkg buffers the entire layer in memory; %s will likely OOM.\n",
				humanBytes(size))
		}
		opts := []registry.Option{}
		if verbose {
			opts = append(opts, registry.Logger(log.New(os.Stderr, "[pkg-registry] ", log.LstdFlags)))
		}
		srv := httptest.NewServer(registry.New(opts...))
		host := strings.TrimPrefix(srv.URL, "http://")
		return host, []name.Option{name.Insecure}, srv.Close

	default:
		// Treat as an explicit registry host.
		nameOpts := []name.Option{}
		if insecure {
			nameOpts = append(nameOpts, name.Insecure)
		}
		return arg, nameOpts, func() {}
	}
}

func reportProgress(generated, pushed, peakHeap *int64, start time.Time, stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	sample := time.NewTicker(250 * time.Millisecond)
	defer sample.Stop()
	var ms runtime.MemStats
	for {
		select {
		case <-stop:
			return
		case <-sample.C:
			runtime.ReadMemStats(&ms)
			if h := int64(ms.HeapAlloc); h > atomic.LoadInt64(peakHeap) {
				atomic.StoreInt64(peakHeap, h)
			}
		case <-ticker.C:
			elapsed := time.Since(start).Seconds()
			g := atomic.LoadInt64(generated)
			p := atomic.LoadInt64(pushed)
			rate := int64(0)
			if elapsed > 0 {
				rate = int64(float64(g) / elapsed)
			}
			fmt.Printf("  ... generated %s | uploaded %s | %s/s | heap %s\n",
				humanBytes(g), humanBytes(p), humanBytes(rate), humanBytes(atomic.LoadInt64(peakHeap)))
		}
	}
}

func parseCompression(s string) (int, error) {
	switch strings.ToLower(s) {
	case "none", "no", "store":
		return gzip.NoCompression, nil
	case "speed", "fast":
		return gzip.BestSpeed, nil
	case "default", "":
		return gzip.DefaultCompression, nil
	case "best", "max":
		return gzip.BestCompression, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("not a level: %q", s)
	}
	if n < gzip.HuffmanOnly || n > gzip.BestCompression {
		return 0, fmt.Errorf("level %d out of range", n)
	}
	return n, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "streampush: "+format+"\n", args...)
	os.Exit(1)
}
