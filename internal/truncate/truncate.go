package truncate

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxChars is the default character limit for tool results sent to the LLM.
	MaxChars = 4000
	// MaxLines is the default line limit for tool results sent to the LLM.
	MaxLines = 200
	// RetentionDuration is how long saved outputs are kept before cleanup.
	RetentionDuration = 7 * 24 * time.Hour
)

// OutputOption configures Output behavior.
type OutputOption func(*outputConfig)

type outputConfig struct {
	canDelegate bool
}

// WithDelegationHint enables the delegation hint when the session supports
// multi-agent task decomposition. When true, truncated results suggest
// delegating to a research agent; when false, they suggest narrowing queries.
func WithDelegationHint(canDelegate bool) OutputOption {
	return func(c *outputConfig) {
		c.canDelegate = canDelegate
	}
}

// Result holds the (possibly truncated) output and metadata.
type Result struct {
	// Content is the text to send to the LLM (truncated if needed).
	Content string
	// Truncated is true if the output was truncated.
	Truncated bool
	// OutputPath is the file path where the full output was saved (empty if not truncated).
	OutputPath string
	// OriginalLen is the original character count.
	OriginalLen int
}

// outputDir returns the directory for saved tool outputs, creating it if needed.
func outputDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".conduit", "tool-output")
	os.MkdirAll(dir, 0755)
	return dir
}

// Output truncates text if it exceeds MaxChars or MaxLines.
// If truncated, the full output is saved to disk and a pointer is included.
// Options can be passed to customize behavior (see WithDelegationHint).
func Output(text string, toolName string, opts ...OutputOption) Result {
	cfg := outputConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	lines := strings.Split(text, "\n")
	originalLen := len(text)

	if len(text) <= MaxChars && len(lines) <= MaxLines {
		return Result{
			Content:     text,
			Truncated:   false,
			OriginalLen: originalLen,
		}
	}

	// Save full output to disk.
	id := fmt.Sprintf("%s_%s", time.Now().Format("20060102-150405"), uuid.New().String()[:8])
	outPath := filepath.Join(outputDir(), id+".txt")

	if err := os.WriteFile(outPath, []byte(text), 0644); err != nil {
		log.Printf("truncate: failed to save output to %s: %v", outPath, err)
		// Fall back to simple truncation without file pointer.
		fallbackHint := "Use more specific queries to narrow the results."
		if cfg.canDelegate {
			fallbackHint = "Consider delegating to a research agent to process the full output."
		}
		truncated := text[:MaxChars] + "\n\n[truncated — " + fallbackHint + "]"
		return Result{
			Content:     truncated,
			Truncated:   true,
			OriginalLen: originalLen,
		}
	}

	// Build truncated preview — take first N lines up to MaxChars.
	var preview strings.Builder
	charCount := 0
	lineCount := 0
	for _, line := range lines {
		if charCount+len(line)+1 > MaxChars || lineCount >= MaxLines {
			break
		}
		if preview.Len() > 0 {
			preview.WriteByte('\n')
			charCount++
		}
		preview.WriteString(line)
		charCount += len(line)
		lineCount++
	}

	remainingLines := len(lines) - lineCount
	remainingBytes := originalLen - preview.Len()

	var actionHint string
	if cfg.canDelegate {
		actionHint = "Consider delegating to a research agent to process the full output. " +
			"The data above shows the shape and structure — a focused sub-agent can analyze the complete result."
	} else {
		actionHint = "Use more specific queries or filters to narrow the results. " +
			"The data above shows the shape and structure — refine your query based on what you see."
	}

	hint := fmt.Sprintf(
		"\n\n... %d more lines (%d bytes) truncated ...\n\n"+
			"Full output saved to: %s\n"+
			"%s",
		remainingLines, remainingBytes, outPath, actionHint,
	)

	return Result{
		Content:     preview.String() + hint,
		Truncated:   true,
		OutputPath:  outPath,
		OriginalLen: originalLen,
	}
}

// Cleanup removes saved outputs older than RetentionDuration.
func Cleanup() {
	dir := outputDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-RetentionDuration)
	var removed int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, entry.Name()))
			removed++
		}
	}
	if removed > 0 {
		log.Printf("truncate: cleaned up %d expired tool outputs", removed)
	}
}
