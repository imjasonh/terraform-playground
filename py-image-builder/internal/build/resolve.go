package build

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Base resolves a base image reference to a v1.Image for the given platform.
// Only the base manifest and config are fetched; its layer blobs are referenced
// by digest and never downloaded.
func Base(ctx context.Context, ref string, platform *v1.Platform, kc authn.Keychain) (v1.Image, error) {
	r, err := name.ParseReference(ref)
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
