package build

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Base resolves a base image reference to a v1.Image for the given platform.
// Only the base manifest and config are fetched; its layer blobs are referenced
// by digest and never downloaded.
func Base(ctx context.Context, ref string, platform *v1.Platform, kc authn.Keychain, nameOpts ...name.Option) (v1.Image, error) {
	r, err := name.ParseReference(ref, nameOpts...)
	if err != nil {
		return nil, fmt.Errorf("build: parse base ref %q: %w", ref, err)
	}
	if kc == nil {
		kc = authn.DefaultKeychain
	}
	opts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(kc),
	}
	if platform != nil {
		opts = append(opts, remote.WithPlatform(*platform))
	}
	img, err := remote.Image(r, opts...)
	if err != nil {
		return nil, fmt.Errorf("build: fetch base %q: %w", ref, err)
	}
	return img, nil
}

// InterpreterVersion reports the Python X.Y advertised by the base image via a
// PYTHON_VERSION env var (set by the official python images and others). When a
// base does not advertise one, ok is false and the caller cannot validate the
// interpreter — a reason to pin a known base rather than rely on a floating
// tag. It reads only the (already-fetched) config, never layer bytes.
func InterpreterVersion(img v1.Image) (major, minor int, ok bool) {
	cf, err := img.ConfigFile()
	if err != nil || cf == nil {
		return 0, 0, false
	}
	for _, kv := range cf.Config.Env {
		k, v, found := strings.Cut(kv, "=")
		if !found || k != "PYTHON_VERSION" {
			continue
		}
		parts := strings.SplitN(v, ".", 3)
		if len(parts) < 2 {
			return 0, 0, false
		}
		maj, err1 := strconv.Atoi(parts[0])
		min, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return maj, min, true
	}
	return 0, 0, false
}
