package cifixer

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// TestToolsUnit tests the tools without making any LLM calls.
func TestToolsUnit(t *testing.T) {
	t.Run("read_file", func(t *testing.T) {
		files := map[string]string{
			"main.go": "package main\n\nfunc main() {}\n",
		}
		tools, _ := CreateMockTools(files)
		tool := tools["read_file"]

		// Test reading existing file
		toolUse := createToolUseBlock("read_file", map[string]any{
			"path": "main.go",
		})
		result := tool.Handler(context.Background(), toolUse, nil, nil)

		if result["error"] != nil {
			t.Errorf("unexpected error: %v", result["error"])
		}
		content, ok := result["content"].(string)
		if !ok || content != files["main.go"] {
			t.Errorf("got content %q, want %q", content, files["main.go"])
		}

		// Test reading non-existent file
		toolUse = createToolUseBlock("read_file", map[string]any{
			"path": "nonexistent.go",
		})
		result = tool.Handler(context.Background(), toolUse, nil, nil)

		if result["error"] == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("write_file", func(t *testing.T) {
		files := map[string]string{
			"main.go": "package main\n",
		}
		tools, mockFS := CreateMockTools(files)
		tool := tools["write_file"]

		newContent := "package main\n\nfunc main() {}\n"
		toolUse := createToolUseBlock("write_file", map[string]any{
			"path":    "main.go",
			"content": newContent,
		})
		result := tool.Handler(context.Background(), toolUse, nil, nil)

		if result["error"] != nil {
			t.Errorf("unexpected error: %v", result["error"])
		}

		// Verify the file was written
		content, ok := mockFS.GetFileContent("main.go")
		if !ok {
			t.Error("file not found after write")
		}
		if content != newContent {
			t.Errorf("got content %q, want %q", content, newContent)
		}

		// Verify it's in changed files
		changed := mockFS.GetChangedFiles()
		if len(changed) != 1 || changed[0] != "main.go" {
			t.Errorf("got changed files %v, want [main.go]", changed)
		}
	})

	t.Run("list_files", func(t *testing.T) {
		files := map[string]string{
			"main.go":     "package main",
			"lib/util.go": "package lib",
		}
		tools, _ := CreateMockTools(files)
		tool := tools["list_files"]

		toolUse := createToolUseBlock("list_files", map[string]any{})
		result := tool.Handler(context.Background(), toolUse, nil, nil)

		if result["error"] != nil {
			t.Errorf("unexpected error: %v", result["error"])
		}

		fileList, ok := result["files"].([]string)
		if !ok {
			t.Error("files not returned as []string")
		}
		if len(fileList) != 2 {
			t.Errorf("got %d files, want 2", len(fileList))
		}
	})
}

// TestRealFileSystemSecurity tests path traversal protection.
func TestRealFileSystemSecurity(t *testing.T) {
	// Create a temp directory for testing
	tmpDir, err := os.MkdirTemp("", "cifixer-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Errorf("failed to remove temp dir: %v", err)
		}
	}()

	fs := NewRealFileSystem(tmpDir)

	// Test path traversal attempts
	traversalPaths := []string{
		"../../../etc/passwd",
		"/etc/passwd",
		"foo/../../../etc/passwd",
		"..\\..\\etc\\passwd",
	}

	for _, path := range traversalPaths {
		t.Run("read_"+path, func(t *testing.T) {
			_, err := fs.ReadFile(path)
			if err == nil {
				t.Errorf("expected error for path traversal: %s", path)
			}
		})

		t.Run("write_"+path, func(t *testing.T) {
			err := fs.WriteFile(path, "malicious content")
			if err == nil {
				t.Errorf("expected error for path traversal: %s", path)
			}
		})
	}
}

// TestCommandRestrictions tests that only allowed commands can be run.
func TestCommandRestrictions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cifixer-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Errorf("failed to remove temp dir: %v", err)
		}
	}()

	fs := NewRealFileSystem(tmpDir)

	// Disallowed commands
	disallowed := []string{"rm", "curl", "wget", "bash", "sh", "cat", "dd"}
	for _, cmd := range disallowed {
		t.Run("disallow_"+cmd, func(t *testing.T) {
			_, err := fs.RunCommand(cmd, nil)
			if err == nil {
				t.Errorf("expected error for disallowed command: %s", cmd)
			}
		})
	}
}

// TestCIContextBinding tests that CIContext properly binds to prompts.
func TestCIContextBinding(t *testing.T) {
	ctx := &CIContext{
		Owner:    "owner",
		Repo:     "repo",
		PRNumber: 123,
		Branch:   "main",
		Turn:     1,
		MaxTurns: 3,
		Failures: []CheckFailure{
			{Name: "build", Conclusion: "failure", Logs: "error: undefined"},
		},
		ChangedFiles: []string{"main.go"},
	}

	prompt, err := UserPromptSimple.BindXML("ci_context", ctx)
	if err != nil {
		t.Fatalf("binding failed: %v", err)
	}

	result, err := prompt.Build()
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Verify the context is included in the prompt
	if !strings.Contains(result, "owner") {
		t.Error("prompt doesn't contain owner")
	}
	if !strings.Contains(result, "repo") {
		t.Error("prompt doesn't contain repo")
	}
	if !strings.Contains(result, "error: undefined") {
		t.Error("prompt doesn't contain failure logs")
	}
}

// TestMockFileSystem tests the mock filesystem implementation.
func TestMockFileSystem(t *testing.T) {
	files := map[string]string{
		"main.go": "package main",
		"lib.go":  "package lib",
	}

	fs := NewMockFileSystem(files)

	// Test reading
	content, err := fs.ReadFile("main.go")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if content != "package main" {
		t.Errorf("got %q, want %q", content, "package main")
	}

	// Test writing
	if err := fs.WriteFile("new.go", "package new"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	content, ok := fs.GetFileContent("new.go")
	if !ok || content != "package new" {
		t.Error("file not written correctly")
	}

	// Test changed files
	changed := fs.GetChangedFiles()
	if len(changed) != 1 || changed[0] != "new.go" {
		t.Errorf("got changed files %v, want [new.go]", changed)
	}

	// Original files map should not be modified
	if _, ok := files["new.go"]; ok {
		t.Error("original files map was modified")
	}
}

// TestEvalTestCasesExist verifies that our eval test cases are defined.
func TestEvalTestCasesExist(t *testing.T) {
	if len(EvalTestCases) == 0 {
		t.Error("no eval test cases defined")
	}

	for _, tc := range EvalTestCases {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Description == "" {
				t.Error("test case has no description")
			}
			if len(tc.Files) == 0 {
				t.Error("test case has no files")
			}
			if tc.CILogs == "" {
				t.Error("test case has no CI logs")
			}
			if len(tc.ExpectedFix) == 0 {
				t.Error("test case has no expected fix")
			}
			if tc.Criterion == "" {
				t.Error("test case has no criterion")
			}
		})
	}
}

// createToolUseBlock creates an anthropic.ToolUseBlock for testing.
func createToolUseBlock(name string, input map[string]any) anthropic.ToolUseBlock {
	inputBytes, _ := json.Marshal(input)
	return anthropic.ToolUseBlock{
		ID:    "test-id",
		Name:  name,
		Input: inputBytes,
	}
}