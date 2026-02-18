// Package risk provides AI-powered risk assessment for GitHub pull requests.
package risk

import (
	"chainguard.dev/driftlessaf/agents/promptbuilder"
)

// Level represents the risk level of a PR.
type Level string

const (
	LevelLow    Level = "low"
	LevelMedium Level = "medium"
	LevelHigh   Level = "high"
)

// PRContext contains the context for a PR risk assessment request.
type PRContext struct {
	Owner        string   `xml:"owner"`
	Repo         string   `xml:"repo"`
	PRNumber     int      `xml:"pr_number"`
	Title        string   `xml:"title"`
	Description  string   `xml:"description"`
	Author       string   `xml:"author"`
	FilesChanged []string `xml:"files_changed>file"`
	Additions    int      `xml:"additions"`
	Deletions    int      `xml:"deletions"`
	// FileContents maps filename to file diff/content for analysis
	FileDiffs map[string]string `xml:"-"`
}

// Bind implements promptbuilder.Bindable.
func (c *PRContext) Bind(prompt *promptbuilder.Prompt) (*promptbuilder.Prompt, error) {
	// Bind the full context as XML
	return prompt.BindXML("pr_context", c)
}

func formatFileDiffs(diffs map[string]string) string {
	var result string
	for filename, diff := range diffs {
		result += "\n--- " + filename + " ---\n"
		result += diff + "\n"
	}
	return result
}

// RiskAssessment is the structured result from the risk assessment agent.
type RiskAssessment struct {
	RiskLevel   string   `json:"risk_level" jsonschema:"description=Risk level: low|medium|high,enum=low,enum=medium,enum=high,required"`
	Reasoning   string   `json:"reasoning" jsonschema:"description=Detailed explanation of the risk assessment,required"`
	RiskyFiles  []string `json:"risky_files" jsonschema:"description=List of files that contribute most to the risk"`
	RiskFactors []string `json:"risk_factors" jsonschema:"description=Specific risk factors identified"`
	Confidence  float64  `json:"confidence" jsonschema:"description=Confidence in the assessment (0.0-1.0),minimum=0,maximum=1"`
}
