// Package cifixer provides an AI-powered agent for fixing CI failures on GitHub PRs.
package cifixer

import (
	"chainguard.dev/driftlessaf/agents/promptbuilder"
)

// CIContext contains the context for a CI fix request.
type CIContext struct {
	Owner        string         `xml:"owner"`
	Repo         string         `xml:"repo"`
	PRNumber     int            `xml:"pr_number"`
	Branch       string         `xml:"branch"`
	Turn         int            `xml:"turn"`
	MaxTurns     int            `xml:"max_turns"`
	Failures     []CheckFailure `xml:"failures>failure"`
	ChangedFiles []string       `xml:"changed_files>file"`
}

// Bind implements promptbuilder.Bindable.
func (c *CIContext) Bind(prompt *promptbuilder.Prompt) (*promptbuilder.Prompt, error) {
	// Bind the full context as XML
	p, err := prompt.BindXML("ci_context", c)
	if err != nil {
		return nil, err
	}
	// Also bind turn and max_turns separately for use outside the XML block
	// Using BindJSON since it accepts any type including integers
	p, err = p.BindJSON("turn", c.Turn)
	if err != nil {
		return nil, err
	}
	return p.BindJSON("max_turns", c.MaxTurns)
}

// CheckFailure represents a single CI check failure.
type CheckFailure struct {
	Name       string `xml:"name"`
	Conclusion string `xml:"conclusion"`
	Logs       string `xml:"logs"`
}

// CIFixResult is the structured result from the CI fixer agent.
type CIFixResult struct {
	Success       bool     `json:"success" jsonschema:"description=Whether the fix was successfully applied"`
	FilesChanged  []string `json:"files_changed" jsonschema:"description=List of files that were modified"`
	CommitMessage string   `json:"commit_message" jsonschema:"description=Commit message for the fix"`
	Reasoning     string   `json:"reasoning" jsonschema:"description=Explanation of the fix or why it could not be applied"`
	NeedsHuman    bool     `json:"needs_human" jsonschema:"description=Whether human intervention is required"`
}

// ReadFileInput is the input for the read_file tool.
type ReadFileInput struct {
	Path string `json:"path" jsonschema:"description=Path to the file relative to repo root,required"`
}

// WriteFileInput is the input for the write_file tool.
type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"description=Path to the file relative to repo root,required"`
	Content string `json:"content" jsonschema:"description=New file content,required"`
}

// ListFilesInput is the input for the list_files tool.
type ListFilesInput struct {
	Path    string `json:"path" jsonschema:"description=Directory path relative to repo root"`
	Pattern string `json:"pattern" jsonschema:"description=Glob pattern to filter files"`
}

// SearchFilesInput is the input for the search_files tool.
type SearchFilesInput struct {
	Pattern string `json:"pattern" jsonschema:"description=Search pattern (regex),required"`
	Path    string `json:"path" jsonschema:"description=Directory to search in"`
}

// RunCommandInput is the input for the run_command tool.
type RunCommandInput struct {
	Command string   `json:"command" jsonschema:"description=Command to run,required"`
	Args    []string `json:"args" jsonschema:"description=Command arguments"`
}
