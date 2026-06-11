package lock

import (
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// uvLockFile is the subset of uv.lock we read: package name/version plus the
// wheel and sdist artifact references. Resolution (which packages to install
// for a target) is delegated to `uv export` — see uvexport.go — so we do not
// model the dependency graph, extras, groups, or markers here.
type uvLockFile struct {
	Package []uvPackage `toml:"package"`
}

type uvPackage struct {
	Name    string       `toml:"name"`
	Version string       `toml:"version"`
	Sdist   uvArtifact   `toml:"sdist"`
	Wheels  []uvArtifact `toml:"wheels"`
}

type uvArtifact struct {
	URL  string `toml:"url"`
	Hash string `toml:"hash"`
}

// parseUVLock reads and unmarshals a uv.lock file.
func parseUVLock(path string) (*uvLockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf uvLockFile
	if err := toml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("uv.lock: parse %q: %w", path, err)
	}
	return &lf, nil
}

func parseUVHash(h string) (string, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return "", fmt.Errorf("missing hash")
	}
	const prefix = "sha256:"
	if !strings.HasPrefix(strings.ToLower(h), prefix) {
		return "", fmt.Errorf("unsupported hash %q (expected sha256:...)", h)
	}
	sum := strings.ToLower(strings.TrimPrefix(strings.ToLower(h), prefix))
	if len(sum) != 64 {
		return "", fmt.Errorf("bad sha256 length in %q", h)
	}
	return sum, nil
}
