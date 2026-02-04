package cifixer

import (
	"context"
	"encoding/json"

	"chainguard.dev/driftlessaf/agents/evals"
	"chainguard.dev/driftlessaf/agents/executor/claudeexecutor"
	"chainguard.dev/driftlessaf/agents/toolcall/claudetool"
	"github.com/anthropics/anthropic-sdk-go"
)

// CreateTools creates the tool definitions for the CI fixer agent.
func CreateTools(fs FileSystem) map[string]claudeexecutor.ToolMetadata[*CIFixResult] {
	return map[string]claudeexecutor.ToolMetadata[*CIFixResult]{
		"read_file":    readFileTool(fs),
		"write_file":   writeFileTool(fs),
		"list_files":   listFilesTool(fs),
		"search_files": searchFilesTool(fs),
	}
}

func readFileTool(fs FileSystem) claudeexecutor.ToolMetadata[*CIFixResult] {
	return claudeexecutor.ToolMetadata[*CIFixResult]{
		Definition: anthropic.ToolParam{
			Name:        "read_file",
			Description: anthropic.String("Read the contents of a file in the repository. Use this to examine source code, configuration files, or any other text file."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file relative to repo root",
					},
				},
				Required: []string{"path"},
			},
		},
		Handler: func(ctx context.Context, toolUse anthropic.ToolUseBlock, trace *evals.Trace[*CIFixResult], result **CIFixResult) map[string]any {
			params, errResp := claudetool.NewParams(toolUse)
			if errResp != nil {
				return errResp
			}

			path, errResp := claudetool.Param[string](params, "path")
			if errResp != nil {
				return errResp
			}

			content, err := fs.ReadFile(path)
			if err != nil {
				return claudetool.Error("failed to read file %q: %v", path, err)
			}

			return map[string]any{
				"content": content,
			}
		},
	}
}

func writeFileTool(fs FileSystem) claudeexecutor.ToolMetadata[*CIFixResult] {
	return claudeexecutor.ToolMetadata[*CIFixResult]{
		Definition: anthropic.ToolParam{
			Name:        "write_file",
			Description: anthropic.String("Write content to a file in the repository. This will overwrite existing files or create new ones. Use this to apply fixes to source code."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file relative to repo root",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "New file content",
					},
				},
				Required: []string{"path", "content"},
			},
		},
		Handler: func(ctx context.Context, toolUse anthropic.ToolUseBlock, trace *evals.Trace[*CIFixResult], result **CIFixResult) map[string]any {
			params, errResp := claudetool.NewParams(toolUse)
			if errResp != nil {
				return errResp
			}

			path, errResp := claudetool.Param[string](params, "path")
			if errResp != nil {
				return errResp
			}

			content, errResp := claudetool.Param[string](params, "content")
			if errResp != nil {
				return errResp
			}

			if err := fs.WriteFile(path, content); err != nil {
				return claudetool.Error("failed to write file %q: %v", path, err)
			}

			return map[string]any{
				"success": true,
				"path":    path,
			}
		},
	}
}

func listFilesTool(fs FileSystem) claudeexecutor.ToolMetadata[*CIFixResult] {
	return claudeexecutor.ToolMetadata[*CIFixResult]{
		Definition: anthropic.ToolParam{
			Name:        "list_files",
			Description: anthropic.String("List files in a directory. Optionally filter by glob pattern (e.g., '*.go', '*.ts'). Use this to explore the repository structure."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Directory path relative to repo root (empty for root)",
					},
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern to filter files (e.g., '*.go')",
					},
				},
			},
		},
		Handler: func(ctx context.Context, toolUse anthropic.ToolUseBlock, trace *evals.Trace[*CIFixResult], result **CIFixResult) map[string]any {
			params, errResp := claudetool.NewParams(toolUse)
			if errResp != nil {
				return errResp
			}

			path, _ := claudetool.OptionalParam[string](params, "path", "")
			pattern, _ := claudetool.OptionalParam[string](params, "pattern", "")

			files, err := fs.ListFiles(path, pattern)
			if err != nil {
				return claudetool.Error("failed to list files: %v", err)
			}

			return map[string]any{
				"files": files,
				"count": len(files),
			}
		},
	}
}

func searchFilesTool(fs FileSystem) claudeexecutor.ToolMetadata[*CIFixResult] {
	return claudeexecutor.ToolMetadata[*CIFixResult]{
		Definition: anthropic.ToolParam{
			Name:        "search_files",
			Description: anthropic.String("Search for a pattern in files using regex. Returns matching lines with file paths and line numbers. Use this to find where specific code or patterns are used."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Search pattern (regex)",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Directory to search in (empty for root)",
					},
				},
				Required: []string{"pattern"},
			},
		},
		Handler: func(ctx context.Context, toolUse anthropic.ToolUseBlock, trace *evals.Trace[*CIFixResult], result **CIFixResult) map[string]any {
			params, errResp := claudetool.NewParams(toolUse)
			if errResp != nil {
				return errResp
			}

			pattern, errResp := claudetool.Param[string](params, "pattern")
			if errResp != nil {
				return errResp
			}

			path, _ := claudetool.OptionalParam[string](params, "path", "")

			matches, err := fs.SearchFiles(pattern, path)
			if err != nil {
				return claudetool.Error("search failed: %v", err)
			}

			return map[string]any{
				"matches": matches,
				"count":   len(matches),
			}
		},
	}
}

// CreateMockTools creates tools that use a MockFileSystem for testing.
func CreateMockTools(files map[string]string) (map[string]claudeexecutor.ToolMetadata[*CIFixResult], *MockFileSystem) {
	mockFS := NewMockFileSystem(files)
	return CreateTools(mockFS), mockFS
}

// MockFileSystem implements FileSystem for testing.
type MockFileSystem struct {
	files        map[string]string
	changedFiles map[string]bool
}

// NewMockFileSystem creates a new MockFileSystem with the given initial files.
func NewMockFileSystem(files map[string]string) *MockFileSystem {
	// Make a copy to avoid mutation
	filesCopy := make(map[string]string, len(files))
	for k, v := range files {
		filesCopy[k] = v
	}
	return &MockFileSystem{
		files:        filesCopy,
		changedFiles: make(map[string]bool),
	}
}

func (m *MockFileSystem) ReadFile(path string) (string, error) {
	content, ok := m.files[path]
	if !ok {
		return "", &fileNotFoundError{path: path}
	}
	return content, nil
}

func (m *MockFileSystem) WriteFile(path, content string) error {
	m.files[path] = content
	m.changedFiles[path] = true
	return nil
}

func (m *MockFileSystem) ListFiles(path, pattern string) ([]string, error) {
	var files []string
	for f := range m.files {
		files = append(files, f)
	}
	return files, nil
}

func (m *MockFileSystem) SearchFiles(pattern, path string) ([]SearchMatch, error) {
	// Simplified search for testing
	return nil, nil
}

func (m *MockFileSystem) RunCommand(command string, args []string) (string, error) {
	return "", &commandNotAllowedError{command: command}
}

func (m *MockFileSystem) GetChangedFiles() []string {
	files := make([]string, 0, len(m.changedFiles))
	for f := range m.changedFiles {
		files = append(files, f)
	}
	return files
}

// GetFileContent returns the current content of a file (for test assertions).
func (m *MockFileSystem) GetFileContent(path string) (string, bool) {
	content, ok := m.files[path]
	return content, ok
}

type fileNotFoundError struct {
	path string
}

func (e *fileNotFoundError) Error() string {
	return "file not found: " + e.path
}

type commandNotAllowedError struct {
	command string
}

func (e *commandNotAllowedError) Error() string {
	return "command not allowed: " + e.command
}

// ToolResultToJSON converts a tool result map to JSON for logging/debugging.
func ToolResultToJSON(result map[string]any) string {
	b, _ := json.MarshalIndent(result, "", "  ")
	return string(b)
}
