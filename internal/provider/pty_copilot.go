package provider

import (
	"os"
	"os/exec"
	"strings"
)

// CopilotAdapter implements CLIAdapter for GitHub Copilot CLI (gh copilot).
// Uses `gh copilot explain` for plain-text responses. No structured JSON output
// is available, so ParseLine treats each line as a text delta.
type CopilotAdapter struct{}

func NewCopilotAdapter() *CopilotAdapter { return &CopilotAdapter{} }

func (a *CopilotAdapter) Name() string { return "copilot" }

func (a *CopilotAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	// gh copilot explain "prompt"
	// No resume support, no system prompt flag, no structured output.
	return []string{"copilot", "explain", prompt}
}

func (a *CopilotAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	text := string(line)

	// gh copilot produces some ANSI formatting and progress indicators;
	// skip lines that are purely decorative.
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "Synthesizing") {
		return nil, nil
	}

	return []StreamEvent{{Type: "delta", Content: text + "\n"}}, nil
}

func (a *CopilotAdapter) Detect() (string, bool) {
	if p := os.Getenv("GH_COPILOT_PATH"); p != "" {
		return p, true
	}
	// Copilot is a `gh` extension. Check that `gh` exists and `copilot` is installed.
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return "", false
	}
	// Verify the copilot extension is available.
	out, err := exec.Command(ghPath, "copilot", "--help").CombinedOutput()
	if err != nil || len(out) == 0 {
		return "", false
	}
	return ghPath, true
}
