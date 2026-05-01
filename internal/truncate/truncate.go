package truncate

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/pkg/models"
)

const (
	// MaxChars is the default character limit for tool results sent to the LLM.
	// Used directly by Output and as the floor by OutputForModel — no model
	// configured, no catalog hit, or a tiny-window model all fall through to
	// this value. CW-20260430-0008 (pilot conversion) introduces the dynamic
	// path; this constant remains the safe baseline.
	MaxChars = 4000
	// MaxLines is the default line limit for tool results sent to the LLM.
	MaxLines = 200
	// MaxCharsCeiling is the upper bound on the dynamic per-model character
	// budget computed by OutputForModel. Caps growth at 32 KB even on a 10M
	// window, so a single tool result can't dominate the conversation slot
	// regardless of how generous the model context window is.
	MaxCharsCeiling = 32000
	// dynMaxCharsPct is the fraction of the model context window (in bytes)
	// used as the proposed per-call truncation budget. 0.4 % of an 800 KB
	// (200K-token) window is 3.2 KB — under the 4 KB floor, so small windows
	// keep the existing behavior. 0.4 % of a 4 MB (1M-token) window is 16 KB,
	// which the ceiling caps at 32 KB. The percentage was chosen so that a
	// 200K window keeps the historical 4 KB cap (rounded up by the floor) and
	// a 1M window unlocks ~4× more headroom — a real expansion without a
	// regression on small-window models.
	dynMaxCharsPct = 0.004
	// RetentionDuration is how long saved outputs are kept before cleanup.
	RetentionDuration = 7 * 24 * time.Hour
)

// computeDynamicMaxChars returns the per-call MaxChars budget for a given
// modelID. modelID is the wire-level identifier (e.g. "claude-sonnet-4-20250514"
// or "gemini-2.5-pro"). When the model is unknown or the catalog has no
// context-window data, returns MaxChars (the static floor). The caller does not
// need to special-case "" — it transparently produces the same result as
// Output for backward compatibility.
//
// The three-step computation is:
//  1. Look up the context window via models.ContextWindowFor (catalog-overlay
//     aware; returns models.DefaultContextWindowTokens on a hard miss).
//  2. Multiply tokens × 4 to convert to an approximate byte budget. The 4×
//     ratio is the project-wide approximation used in
//     internal/context/window.go:179 and elsewhere.
//  3. Take dynMaxCharsPct of the byte budget; clamp to [MaxChars,
//     MaxCharsCeiling].
//
// Returns the effective MaxChars in bytes, plus the resolved window in tokens
// so callers can include it in telemetry.
func computeDynamicMaxChars(modelID string) (effective int, windowTokens int) {
	if modelID == "" {
		return MaxChars, 0
	}
	windowTokens = models.ContextWindowFor(modelID)
	if windowTokens <= 0 {
		return MaxChars, 0
	}
	windowBytes := windowTokens * 4
	proposed := int(float64(windowBytes) * dynMaxCharsPct)
	if proposed < MaxChars {
		return MaxChars, windowTokens
	}
	if proposed > MaxCharsCeiling {
		return MaxCharsCeiling, windowTokens
	}
	return proposed, windowTokens
}

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
	dir := filepath.Join(home, ".nanite", "tool-output")
	os.MkdirAll(dir, 0755)
	return dir
}

// Output truncates text if it exceeds MaxChars or MaxLines.
// If truncated, the full output is saved to disk and a pointer is included.
// Options can be passed to customize behavior (see WithDelegationHint).
//
// For model-aware sizing (CW-20260430-0008 pilot), prefer OutputForModel.
// Output is preserved for callers that don't have a modelID in scope (shell,
// code_exec) and for tests that pin behavior at the static MaxChars floor.
func Output(text string, toolName string, opts ...OutputOption) Result {
	return outputWithCap(text, toolName, MaxChars, "", 0, opts...)
}

// OutputForModel is the model-aware variant of Output. The character limit is
// computed from the model's context window via pkg/models.ContextWindowFor:
// roughly 0.4 % of the window in bytes, clamped to [MaxChars, MaxCharsCeiling].
// When modelID is empty or the model has no catalog entry, the floor is used —
// behavior is identical to Output.
//
// CW-20260430-0008 (P2 pilot). The 4 KB static cap was sized for 200K-token
// windows; on Gemini 2.5 Pro's 1M window the same percentage of available
// context is 16 KB. Without this scaling, large-window models pay the same
// truncation tax as small-window models even though the conversation slot can
// absorb 5× more content.
func OutputForModel(text string, toolName string, modelID string, opts ...OutputOption) Result {
	maxChars, windowTokens := computeDynamicMaxChars(modelID)
	return outputWithCap(text, toolName, maxChars, modelID, windowTokens, opts...)
}

// outputWithCap is the shared implementation for Output and OutputForModel. The
// maxChars parameter is the per-call MaxChars budget (bytes). modelID and
// windowTokens are passed through for telemetry only — they have no effect on
// trimming behavior.
func outputWithCap(text string, toolName string, maxChars int, modelID string, windowTokens int, opts ...OutputOption) Result {
	cfg := outputConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	lines := strings.Split(text, "\n")
	originalLen := len(text)

	if len(text) <= maxChars && len(lines) <= MaxLines {
		return Result{
			Content:     text,
			Truncated:   false,
			OriginalLen: originalLen,
		}
	}

	// Telemetry — log when the dynamic cap actually trims a result. Only fires
	// when the caller went through the model-aware path AND the result was
	// over the cap; the static path stays quiet (callers without a model
	// already have the existing "tool result truncated" log in
	// chat_tool_executor.go). CW-20260430-0008 pilot — operators can grep
	// either log line to attribute trims.
	if modelID != "" && windowTokens > 0 {
		slog.Info("truncate: dynamic cap trimmed",
			"tool", toolName,
			"model", modelID,
			"window_tokens", windowTokens,
			"effective_max_chars", maxChars,
			"original_len", originalLen,
		)
	}

	// Save full output to disk.
	id := fmt.Sprintf("%s_%s", time.Now().Format("20060102-150405"), uuid.New().String()[:8])
	outPath := filepath.Join(outputDir(), id+".txt")

	if err := os.WriteFile(outPath, []byte(text), 0644); err != nil {
		slog.Warn("truncate: failed to save output", "path", outPath, "err", err)
		// Fall back to simple truncation without file pointer.
		fallbackHint := "Use more specific queries to narrow the results."
		if cfg.canDelegate {
			fallbackHint = "Consider delegating to a research agent to process the full output."
		}
		truncated := text[:maxChars] + "\n\n[truncated — " + fallbackHint + "]"
		return Result{
			Content:     truncated,
			Truncated:   true,
			OriginalLen: originalLen,
		}
	}

	// Build truncated preview — take first N lines up to maxChars.
	var preview strings.Builder
	charCount := 0
	lineCount := 0
	for _, line := range lines {
		if charCount+len(line)+1 > maxChars || lineCount >= MaxLines {
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

	// CW-20260419-0014 (user-reported via c17): do NOT include the on-disk
	// file path in the LLM-visible hint. The LLM was mis-using it as a
	// `fetch_tool_result` id and getting confused when the cache lookup
	// failed. The file is still saved (for operator debugging via
	// `~/.nanite/tool-output/`) but the LLM should not see the path.
	// Large results have a proper cache_id via ResultCache's pointer
	// footer — that's the intended retrieval mechanism.
	hint := fmt.Sprintf(
		"\n\n... %d more lines (%d bytes) truncated ...\n\n%s",
		remainingLines, remainingBytes, actionHint,
	)
	_ = outPath // still saved for operator debugging; not surfaced to LLM

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
		slog.Info("truncate: cleaned up expired tool outputs", "removed", removed)
	}
}
