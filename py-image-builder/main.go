// Command py-image-builder builds and pushes OCI images for Python applications
// without a Docker daemon, in the spirit of ko.
//
// It installs each resolved wheel into its own deterministic, content-addressed
// layer (so adding a dependency creates exactly one new layer and unchanged
// dependencies are never re-uploaded) and lays the application source on top.
package main

import (
	"fmt"
	"os"

	"github.com/imjasonh/terraform-playground/py-image-builder/internal/cli"
)

func main() {
	if err := cli.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
