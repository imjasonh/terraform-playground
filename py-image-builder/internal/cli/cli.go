// Package cli implements the py-image-builder command line.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"

	"github.com/imjasonh/terraform-playground/py-image-builder/internal/build"
	"github.com/imjasonh/terraform-playground/py-image-builder/internal/lock"
	"github.com/imjasonh/terraform-playground/py-image-builder/internal/sbom"
	"github.com/imjasonh/terraform-playground/py-image-builder/internal/wheel"
	"github.com/imjasonh/terraform-playground/py-image-builder/internal/wheelhouse"
)

// Root returns the root cobra command.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "py-image-builder",
		Short:         "Docker-less, layer-aware Python container image builder",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(buildCmd())
	return root
}

type buildFlags struct {
	base       string
	lockFile   string
	findLinks  []string
	source     string
	tag        string
	platform   string
	pythonTag  string
	prefix     string
	workingDir string
	entrypoint []string
	cmd        []string
	env        []string
	user       string
	labels     []string
	ignore     []string
	strategy   string

	push        bool
	ociLayout   string
	printDigest bool
	sbomOut     string
	requireHash bool
}

func buildCmd() *cobra.Command {
	f := &buildFlags{}
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a Python application image and push it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBuild(cmd.Context(), f)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&f.base, "base", "", "base image reference (required)")
	fs.StringVar(&f.lockFile, "lock", "", "hashed requirements file (required)")
	fs.StringArrayVar(&f.findLinks, "find-links", nil, "directory of wheels to resolve against (repeatable, required)")
	fs.StringVar(&f.source, "source", "", "application source directory (optional)")
	fs.StringVarP(&f.tag, "tag", "t", "", "image tag to push, e.g. registry/repo:tag")
	fs.StringVar(&f.platform, "platform", "", "platform for base resolution, e.g. linux/amd64")
	fs.StringVar(&f.pythonTag, "python", "python3.12", "python lib directory name for site-packages")
	fs.StringVar(&f.prefix, "prefix", "/app/.venv", "install prefix (venv-like root)")
	fs.StringVar(&f.workingDir, "workdir", "/app", "image working directory and source destination")
	fs.StringArrayVar(&f.entrypoint, "entrypoint", nil, "entrypoint argv (repeatable)")
	fs.StringArrayVar(&f.cmd, "cmd", nil, "cmd argv (repeatable)")
	fs.StringArrayVar(&f.env, "env", nil, "extra KEY=VALUE env (repeatable)")
	fs.StringVar(&f.user, "user", "", "image user, e.g. 65532")
	fs.StringArrayVar(&f.labels, "label", nil, "image label KEY=VALUE (repeatable)")
	fs.StringArrayVar(&f.ignore, "ignore", nil, "extra source ignore glob (repeatable)")
	fs.StringVar(&f.strategy, "layer-strategy", string(build.PerWheel), "per-wheel | single-deps-layer")
	fs.BoolVar(&f.push, "push", true, "push the image to --tag")
	fs.StringVar(&f.ociLayout, "oci-layout", "", "also write the image to this OCI layout directory")
	fs.BoolVar(&f.printDigest, "print-digest", false, "print only the resulting image digest")
	fs.StringVar(&f.sbomOut, "sbom", "", "write a CycloneDX SBOM to this path")
	fs.BoolVar(&f.requireHash, "require-hashes", true, "require every requirement to carry --hash entries")
	return cmd
}

func runBuild(ctx context.Context, f *buildFlags) error {
	if f.base == "" {
		return fmt.Errorf("--base is required")
	}
	if f.lockFile == "" {
		return fmt.Errorf("--lock is required")
	}
	if len(f.findLinks) == 0 {
		return fmt.Errorf("--find-links is required (a directory of wheels)")
	}

	reqs, err := lock.ParseFile(f.lockFile)
	if err != nil {
		return err
	}
	if err := lock.Validate(reqs, f.requireHash); err != nil {
		return err
	}
	wheels, err := wheelhouse.Resolve(reqs, f.findLinks)
	if err != nil {
		return err
	}

	var platform *v1.Platform
	if f.platform != "" {
		platform, err = v1.ParsePlatform(f.platform)
		if err != nil {
			return err
		}
	}

	base, err := resolveBase(ctx, f.base, platform)
	if err != nil {
		return err
	}

	labels, err := keyValues(f.labels)
	if err != nil {
		return fmt.Errorf("--label: %w", err)
	}

	img, err := build.Build(build.Options{
		Base:       base,
		Wheels:     wheels,
		Layout:     wheel.Layout{Prefix: f.prefix, PythonTag: f.pythonTag},
		Strategy:   build.LayerStrategy(f.strategy),
		SourceDir:  f.source,
		Ignore:     f.ignore,
		WorkingDir: f.workingDir,
		Entrypoint: f.entrypoint,
		Cmd:        f.cmd,
		Env:        f.env,
		User:       f.user,
		Labels:     labels,
	})
	if err != nil {
		return err
	}

	if f.sbomOut != "" {
		data, err := sbom.Generate(wheels)
		if err != nil {
			return err
		}
		if err := os.WriteFile(f.sbomOut, data, 0o644); err != nil {
			return err
		}
	}

	digest, err := img.Digest()
	if err != nil {
		return err
	}

	if f.ociLayout != "" {
		if _, err := layout.Write(f.ociLayout, empty.Index); err != nil {
			return err
		}
		p, err := layout.FromPath(f.ociLayout)
		if err != nil {
			return err
		}
		if err := p.AppendImage(img); err != nil {
			return err
		}
	}

	if f.printDigest {
		fmt.Println(digest.String())
		return nil
	}

	if f.push {
		if f.tag == "" {
			return fmt.Errorf("--tag is required to push (or use --push=false)")
		}
		ref, err := name.ParseReference(f.tag)
		if err != nil {
			return err
		}
		if err := remote.Write(ref, img,
			remote.WithContext(ctx),
			remote.WithAuthFromKeychain(authn.DefaultKeychain),
		); err != nil {
			return fmt.Errorf("push: %w", err)
		}
		fmt.Printf("pushed %s@%s\n", ref.Context().Name(), digest)
		return nil
	}

	fmt.Println(digest.String())
	return nil
}

func resolveBase(ctx context.Context, ref string, platform *v1.Platform) (v1.Image, error) {
	if ref == "scratch" {
		return empty.Image, nil
	}
	return build.Base(ctx, ref, platform, authn.DefaultKeychain)
}

func keyValues(in []string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range in {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", kv)
		}
		out[k] = v
	}
	return out, nil
}
