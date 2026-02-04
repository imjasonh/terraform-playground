package cifixer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileSystem provides file operations for the CI fixer agent.
// This interface allows for mocking in tests.
type FileSystem interface {
	ReadFile(path string) (string, error)
	WriteFile(path, content string) error
	ListFiles(path, pattern string) ([]string, error)
	SearchFiles(pattern, path string) ([]SearchMatch, error)
	RunCommand(command string, args []string) (string, error)
	GetChangedFiles() []string
}

// SearchMatch represents a search result.
type SearchMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// RealFileSystem implements FileSystem using the actual file system.
type RealFileSystem struct {
	root         string
	changedFiles map[string]bool
	allowedCmds  map[string]bool
}

// NewRealFileSystem creates a new RealFileSystem rooted at the given directory.
func NewRealFileSystem(root string) *RealFileSystem {
	return &RealFileSystem{
		root:         root,
		changedFiles: make(map[string]bool),
		allowedCmds: map[string]bool{
			"go":      true,
			"npm":     true,
			"npx":     true,
			"yarn":    true,
			"make":    true,
			"cargo":   true,
			"rustfmt": true,
			"clippy":  true,
			"python":  true,
			"pip":     true,
			"uv":      true,
			"gofmt":   true,
			"prettier": true,
			"eslint":  true,
		},
	}
}

// resolvePath resolves a relative path to an absolute path within the root.
// It returns an error if the path escapes the root directory.
func (r *RealFileSystem) resolvePath(path string) (string, error) {
	// Clean the path
	clean := filepath.Clean(path)

	// Reject absolute paths
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute paths not allowed: %s", path)
	}

	// Reject paths that try to escape
	if strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("path traversal not allowed: %s", path)
	}

	// Join with root
	full := filepath.Join(r.root, clean)

	// Double-check the resolved path is under root
	rel, err := filepath.Rel(r.root, full)
	if err != nil {
		return "", fmt.Errorf("invalid path: %s", path)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes root: %s", path)
	}

	return full, nil
}

func (r *RealFileSystem) ReadFile(path string) (string, error) {
	full, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}

	return string(content), nil
}

func (r *RealFileSystem) WriteFile(path, content string) error {
	full, err := r.resolvePath(path)
	if err != nil {
		return err
	}

	// Create parent directories if needed
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	// Track the change
	r.changedFiles[path] = true

	return nil
}

func (r *RealFileSystem) ListFiles(path, pattern string) ([]string, error) {
	dir := r.root
	if path != "" {
		var err error
		dir, err = r.resolvePath(path)
		if err != nil {
			return nil, err
		}
	}

	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden directories
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path
		rel, err := filepath.Rel(r.root, p)
		if err != nil {
			return nil
		}

		// Match pattern if provided
		if pattern != "" {
			matched, err := filepath.Match(pattern, d.Name())
			if err != nil {
				return err
			}
			if !matched {
				return nil
			}
		}

		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}

	return files, nil
}

func (r *RealFileSystem) SearchFiles(pattern, path string) ([]SearchMatch, error) {
	dir := r.root
	if path != "" {
		var err error
		dir, err = r.resolvePath(path)
		if err != nil {
			return nil, err
		}
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	var matches []SearchMatch
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden directories and common non-source directories
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary files by extension
		ext := filepath.Ext(d.Name())
		if isBinaryExt(ext) {
			return nil
		}

		content, err := os.ReadFile(p)
		if err != nil {
			return nil // Skip files we can't read
		}

		lines := strings.Split(string(content), "\n")
		rel, _ := filepath.Rel(r.root, p)

		for i, line := range lines {
			if re.MatchString(line) {
				matches = append(matches, SearchMatch{
					File:    rel,
					Line:    i + 1,
					Content: line,
				})
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("searching files: %w", err)
	}

	// Limit results to prevent overwhelming the agent
	if len(matches) > 100 {
		matches = matches[:100]
	}

	return matches, nil
}

func (r *RealFileSystem) RunCommand(command string, args []string) (string, error) {
	// Only allow specific commands
	if !r.allowedCmds[command] {
		return "", fmt.Errorf("command not allowed: %s (allowed: go, npm, npx, yarn, make, cargo, rustfmt, python, pip, uv, gofmt, prettier, eslint)", command)
	}

	// For now, return an error - we'll implement this when we have proper sandboxing
	return "", fmt.Errorf("command execution not yet implemented for security reasons")
}

func (r *RealFileSystem) GetChangedFiles() []string {
	files := make([]string, 0, len(r.changedFiles))
	for f := range r.changedFiles {
		files = append(files, f)
	}
	return files
}

func isBinaryExt(ext string) bool {
	binary := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
		".zip": true, ".tar": true, ".gz": true, ".rar": true,
		".pdf": true, ".doc": true, ".docx": true,
		".bin": true, ".o": true, ".a": true,
	}
	return binary[strings.ToLower(ext)]
}
