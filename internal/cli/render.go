package cli

import (
	"fmt"
	"os"
	"strings"
)

// Renderer handles terminal output for streaming chat responses.
type Renderer struct {
	// NoColor disables ANSI color output.
	NoColor bool
	// ShowUsage displays token usage after each response.
	ShowUsage bool
}

// NewRenderer creates a renderer with default settings.
func NewRenderer() *Renderer {
	return &Renderer{
		ShowUsage: true,
	}
}

// color returns the ANSI escape sequence if color is enabled.
func (r *Renderer) color(code string) string {
	if r.NoColor {
		return ""
	}
	return code
}

const (
	colorReset  = "\033[0m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
	colorCyan   = "\033[36m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
)

// RenderEvent processes a single stream event and writes to stdout.
// Returns false if the stream is complete.
func (r *Renderer) RenderEvent(evt StreamEvent) bool {
	switch evt.Type {
	case "stream_start":
		// Silent — stream begins.
		return true

	case "delta":
		fmt.Print(evt.Content)
		return true

	case "tool_call":
		fmt.Fprintf(os.Stderr, "\n%s  Tool: %s%s\n",
			r.color(colorDim+colorCyan), evt.Tool, r.color(colorReset))
		return true

	case "tool_result":
		if evt.Summary != "" {
			// Show a compact summary of the tool result.
			summary := evt.Summary
			if len(summary) > 120 {
				summary = summary[:117] + "..."
			}
			fmt.Fprintf(os.Stderr, "%s  → %s%s\n",
				r.color(colorDim), summary, r.color(colorReset))
		}
		return true

	case "status":
		fmt.Fprintf(os.Stderr, "%s  %s%s\n",
			r.color(colorDim+colorYellow), evt.Content, r.color(colorReset))
		return true

	case "circuit_open":
		fmt.Fprintf(os.Stderr, "\n%s⚡ Rate limited: %s%s\n",
			r.color(colorYellow), evt.Content, r.color(colorReset))
		return true

	case "error":
		fmt.Fprintf(os.Stderr, "\n%sError: %s%s\n",
			r.color(colorRed), evt.Error, r.color(colorReset))
		return true

	case "stream_end":
		fmt.Println() // Newline after streamed content.
		if r.ShowUsage && evt.Usage != nil {
			fmt.Fprintf(os.Stderr, "%s[%d in / %d out tokens]%s\n",
				r.color(colorDim), evt.Usage.InputTokens, evt.Usage.OutputTokens, r.color(colorReset))
		}
		return false // Stream complete.

	default:
		return true
	}
}

// PrintPrompt writes the REPL prompt.
func (r *Renderer) PrintPrompt(agentName string) {
	fmt.Fprintf(os.Stderr, "\n%s%s>%s ",
		r.color(colorBold+colorGreen), agentName, r.color(colorReset))
}

// PrintInfo writes an informational message.
func (r *Renderer) PrintInfo(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s%s%s\n", r.color(colorDim), msg, r.color(colorReset))
}

// PrintError writes an error message.
func (r *Renderer) PrintError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s%s%s\n", r.color(colorRed), msg, r.color(colorReset))
}

// PrintBanner writes the startup banner.
func (r *Renderer) PrintBanner(version, serverURL string) {
	fmt.Fprintf(os.Stderr, "%sMentat CLI%s — connected to %s\n",
		r.color(colorBold), r.color(colorReset), serverURL)
	fmt.Fprintf(os.Stderr, "%sType a message to chat. Commands: /quit, /session, /mode%s\n",
		r.color(colorDim), r.color(colorReset))
	fmt.Fprintln(os.Stderr, strings.Repeat("─", 60))
}
