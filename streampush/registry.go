package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// sinkRegistry is a minimal OCI distribution registry that supports the
// incremental ("chunked") blob upload protocol WITHOUT ever buffering the full
// blob in memory.
//
// The default in-memory registry shipped with go-containerregistry
// (pkg/registry) speaks the same protocol, but its PATCH handler does
// `io.Copy(&bytes.Buffer{}, req.Body)` and its blob handler stores the whole
// blob in a map[string][]byte. That makes it impossible to receive a 200GB
// layer without 200GB of RAM.
//
// sinkRegistry instead streams the PATCH/PUT body straight through a sha256
// hasher into io.Discard, keeping only the running digest and byte count. Blobs
// smaller than smallBlobLimit (config, manifests) are retained so the resulting
// image is still describable/pullable; larger blobs are digested and dropped.
type sinkRegistry struct {
	mu         sync.Mutex
	blobSizes  map[string]int64  // digest -> size (everything we've committed)
	smallBlobs map[string][]byte // digest -> content (only blobs <= smallBlobLimit)
	uploads    map[string]*upload
	manifests  map[string][]byte // repo+"/"+reference -> raw manifest
	manifestCT map[string]string // repo+"/"+reference -> content type

	smallBlobLimit int64
	verbose        bool
}

type upload struct {
	h     hash.Hash
	buf   *bytes.Buffer // retained prefix, capped at limit
	limit int64
	n     int64
}

func newUpload(limit int64) *upload {
	return &upload{h: sha256.New(), buf: &bytes.Buffer{}, limit: limit}
}

// Write streams bytes through the digest while retaining at most `limit` bytes
// of prefix. This is the crux of the non-buffering design: io.Copy hands us the
// whole body, but we never hold more than `limit` of it in memory.
func (u *upload) Write(p []byte) (int, error) {
	u.h.Write(p)
	if room := u.limit - int64(u.buf.Len()); room > 0 {
		if int64(len(p)) <= room {
			u.buf.Write(p)
		} else {
			u.buf.Write(p[:room])
		}
	}
	u.n += int64(len(p))
	return len(p), nil
}

// retained returns the full blob content if it fit entirely within the cap, and
// false if it was too large to keep (and was therefore digested-and-dropped).
func (u *upload) retained() ([]byte, bool) {
	if u.n <= u.limit {
		return u.buf.Bytes(), true
	}
	return nil, false
}

func newSinkRegistry(smallBlobLimit int64, verbose bool) *sinkRegistry {
	return &sinkRegistry{
		blobSizes:      map[string]int64{},
		smallBlobs:     map[string][]byte{},
		uploads:        map[string]*upload{},
		manifests:      map[string][]byte{},
		manifestCT:     map[string]string{},
		smallBlobLimit: smallBlobLimit,
		verbose:        verbose,
	}
}

func (s *sinkRegistry) logf(format string, args ...any) {
	if s.verbose {
		log.Printf("[sink-registry] "+format, args...)
	}
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"errors":[{"code":%q,"message":%q}]}`, code, msg)
}

func (s *sinkRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")

	if r.URL.Path == "/v2" || r.URL.Path == "/v2/" {
		w.WriteHeader(http.StatusOK)
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "v2" {
		writeErr(w, http.StatusNotFound, "NAME_UNKNOWN", "unsupported path")
		return
	}

	// Find the "blobs" or "manifests" marker.
	marker, idx := "", -1
	for i := len(parts) - 1; i >= 1; i-- {
		if parts[i] == "blobs" || parts[i] == "manifests" {
			marker, idx = parts[i], i
			break
		}
	}
	if idx == -1 {
		writeErr(w, http.StatusNotFound, "NAME_UNKNOWN", "expected blobs or manifests in path")
		return
	}
	repo := strings.Join(parts[1:idx], "/")
	rest := parts[idx+1:]

	switch marker {
	case "blobs":
		s.handleBlobs(w, r, repo, rest)
	case "manifests":
		if len(rest) != 1 {
			writeErr(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "bad manifest path")
			return
		}
		s.handleManifests(w, r, repo, rest[0])
	}
}

func (s *sinkRegistry) handleBlobs(w http.ResponseWriter, r *http.Request, repo string, rest []string) {
	// Upload sub-resource: /v2/<repo>/blobs/uploads[/<id>]
	if len(rest) >= 1 && rest[0] == "uploads" {
		switch r.Method {
		case http.MethodPost:
			s.startUpload(w, r, repo)
		case http.MethodPatch:
			if len(rest) < 2 {
				writeErr(w, http.StatusBadRequest, "BLOB_UPLOAD_INVALID", "missing upload id")
				return
			}
			s.patchUpload(w, r, repo, rest[1])
		case http.MethodPut:
			if len(rest) < 2 {
				writeErr(w, http.StatusBadRequest, "BLOB_UPLOAD_INVALID", "missing upload id")
				return
			}
			s.putUpload(w, r, repo, rest[1])
		default:
			writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed on uploads")
		}
		return
	}

	// Blob by digest: /v2/<repo>/blobs/<digest>
	if len(rest) != 1 {
		writeErr(w, http.StatusNotFound, "BLOB_UNKNOWN", "bad blob path")
		return
	}
	digest := rest[0]
	switch r.Method {
	case http.MethodHead, http.MethodGet:
		s.getBlob(w, r, digest)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed on blob")
	}
}

func (s *sinkRegistry) startUpload(w http.ResponseWriter, r *http.Request, repo string) {
	// Monolithic upload: POST .../uploads/?digest=sha256:...
	if digest := r.URL.Query().Get("digest"); digest != "" {
		u := newUpload(s.smallBlobLimit)
		s.consume(u, r.Body)
		s.finalizeBlob(w, u, digest)
		return
	}

	id := strconv.FormatInt(rand.Int63(), 16) + strconv.FormatInt(rand.Int63(), 16)
	s.mu.Lock()
	s.uploads[id] = newUpload(s.smallBlobLimit)
	s.mu.Unlock()
	s.logf("POST start upload repo=%s id=%s", repo, id)

	loc := fmt.Sprintf("/v2/%s/blobs/uploads/%s", repo, id)
	w.Header().Set("Location", loc)
	w.Header().Set("Range", "0-0")
	w.Header().Set("Docker-Upload-Uuid", id)
	w.WriteHeader(http.StatusAccepted)
}

func (s *sinkRegistry) patchUpload(w http.ResponseWriter, r *http.Request, repo, id string) {
	s.mu.Lock()
	u := s.uploads[id]
	s.mu.Unlock()
	if u == nil {
		writeErr(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "no such upload")
		return
	}

	// Stream the body straight through the hasher; never hold the whole blob.
	n := s.consume(u, r.Body)
	s.logf("PATCH upload id=%s chunk=%d total=%d", id, n, u.n)

	loc := fmt.Sprintf("/v2/%s/blobs/uploads/%s", repo, id)
	w.Header().Set("Location", loc)
	w.Header().Set("Range", fmt.Sprintf("0-%d", maxInt64(u.n-1, 0)))
	w.Header().Set("Docker-Upload-Uuid", id)
	w.WriteHeader(http.StatusAccepted)
}

func (s *sinkRegistry) putUpload(w http.ResponseWriter, r *http.Request, repo, id string) {
	digest := r.URL.Query().Get("digest")
	if digest == "" {
		writeErr(w, http.StatusBadRequest, "DIGEST_INVALID", "missing digest on commit")
		return
	}

	s.mu.Lock()
	u := s.uploads[id]
	delete(s.uploads, id)
	s.mu.Unlock()
	if u == nil {
		writeErr(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "no such upload")
		return
	}

	// Any trailing bytes on the PUT are also streamed through.
	s.consume(u, r.Body)
	s.logf("PUT commit id=%s digest=%s size=%d", id, digest, u.n)
	s.finalizeBlob(w, u, digest)
}

// consume streams rc through the upload (digesting + capped retention) using a
// fixed-size staging buffer. Memory stays constant no matter how big rc is.
func (s *sinkRegistry) consume(u *upload, rc io.Reader) int64 {
	before := u.n
	// io.Copy uses a 32KiB staging buffer; u.Write caps what it retains.
	_, _ = io.Copy(u, rc)
	return u.n - before
}

func (s *sinkRegistry) finalizeBlob(w http.ResponseWriter, u *upload, expected string) {
	got := "sha256:" + hex.EncodeToString(u.h.Sum(nil))
	if expected != got {
		writeErr(w, http.StatusBadRequest, "DIGEST_INVALID",
			fmt.Sprintf("digest mismatch: client said %s, computed %s", expected, got))
		return
	}

	s.mu.Lock()
	s.blobSizes[got] = u.n
	if content, ok := u.retained(); ok {
		s.smallBlobs[got] = append([]byte(nil), content...)
	}
	s.mu.Unlock()

	w.Header().Set("Docker-Content-Digest", got)
	w.WriteHeader(http.StatusCreated)
}

func (s *sinkRegistry) getBlob(w http.ResponseWriter, r *http.Request, digest string) {
	s.mu.Lock()
	size, known := s.blobSizes[digest]
	content, haveContent := s.smallBlobs[digest]
	s.mu.Unlock()

	if !known {
		writeErr(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob not found")
		return
	}

	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !haveContent {
		// We digested-and-dropped this (large) blob; we can't replay it.
		writeErr(w, http.StatusNotFound, "BLOB_UNKNOWN",
			"blob content was discarded by the streaming sink (too large to retain)")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *sinkRegistry) handleManifests(w http.ResponseWriter, r *http.Request, repo, ref string) {
	key := repo + "/" + ref
	switch r.Method {
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "MANIFEST_INVALID", err.Error())
			return
		}
		sum := sha256.Sum256(body)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		ct := r.Header.Get("Content-Type")

		s.mu.Lock()
		s.manifests[key] = body
		s.manifests[repo+"/"+digest] = body
		s.manifestCT[key] = ct
		s.manifestCT[repo+"/"+digest] = ct
		s.mu.Unlock()
		s.logf("PUT manifest repo=%s ref=%s digest=%s size=%d", repo, ref, digest, len(body))

		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)

	case http.MethodHead, http.MethodGet:
		s.mu.Lock()
		body, ok := s.manifests[key]
		ct := s.manifestCT[key]
		s.mu.Unlock()
		if !ok {
			writeErr(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest not found")
			return
		}
		sum := sha256.Sum256(body)
		if ct == "" {
			ct = "application/vnd.docker.distribution.manifest.v2+json"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(sum[:]))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)

	default:
		writeErr(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed on manifest")
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
