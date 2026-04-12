package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	pluginsdk "github.com/hollis-labs/go-plugin"

	"github.com/hollis-labs/nanite/internal/safego"
)

// UtilityCallFunc is a function that makes a lightweight LLM call for extraction.
// It takes a prompt string and returns the LLM response.
type UtilityCallFunc func(ctx context.Context, prompt string) (string, error)

// Extractor handles memory extraction from chat messages and compaction events.
// It registers as event hooks on message.received and context.compacted.
type Extractor struct {
	service     *Service
	utilityCall UtilityCallFunc
	defaultUser string // fallback user ID for namespace scoping
}

// NewExtractor creates a memory extractor wired to the given service and utility LLM.
func NewExtractor(svc *Service, utilityCall UtilityCallFunc) *Extractor {
	return &Extractor{
		service:     svc,
		utilityCall: utilityCall,
		defaultUser: "default",
	}
}

// SetDefaultUser sets the default user ID used for namespace scoping.
func (e *Extractor) SetDefaultUser(userID string) {
	e.defaultUser = userID
}

// memorySignalPatterns are regex patterns that indicate a message contains
// something worth remembering. Checked before calling the LLM to avoid
// unnecessary utility calls on every turn.
var memorySignalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bremember\b`),
	regexp.MustCompile(`(?i)\bdon'?t forget\b`),
	regexp.MustCompile(`(?i)\balways\b`),
	regexp.MustCompile(`(?i)\bnever\b`),
	regexp.MustCompile(`(?i)\bi prefer\b`),
	regexp.MustCompile(`(?i)\bfrom now on\b`),
	regexp.MustCompile(`(?i)\bin the future\b`),
	regexp.MustCompile(`(?i)\bno,?\s+(not|don'?t)\b`),
	regexp.MustCompile(`(?i)\bthat'?s (wrong|incorrect)\b`),
	regexp.MustCompile(`(?i)\bactually,?\s`),
	regexp.MustCompile(`(?i)\binstead,?\s`),
	regexp.MustCompile(`(?i)\bstop doing\b`),
	regexp.MustCompile(`(?i)\buse .+ instead\b`),
}

// HasMemorySignal checks whether a message contains explicit signals that
// indicate something should be remembered. This is the gate that prevents
// the LLM from being called on every turn.
func HasMemorySignal(content string) bool {
	// Only check first 2000 chars to avoid regex on huge messages.
	check := content
	if len(check) > 2000 {
		check = check[:2000]
	}
	for _, pat := range memorySignalPatterns {
		if pat.MatchString(check) {
			return true
		}
	}
	return false
}

// perTurnHook implements pluginsdk.EventHook for per-turn memory extraction.
type perTurnHook struct {
	extractor *Extractor
}

func (h *perTurnHook) Handle(ctx context.Context, event pluginsdk.Event) error {
	// Only process user messages.
	role, _ := event.Data["role"].(string)
	if role != "" && role != "user" {
		return nil
	}

	content, _ := event.Data["content"].(string)
	if content == "" {
		return nil
	}

	// Gate: only extract if explicit signals detected.
	if !HasMemorySignal(content) {
		return nil
	}

	sessionID := event.SessionID
	if sessionID == "" {
		sessionID, _ = event.Data["session_id"].(string)
	}

	// Fire-and-forget: don't block the message flow.
	safego.Go(ctx, "memory.extractor.perTurn", func() {
		h.extractor.extractPerTurn(sessionID, content)
	})
	return nil
}

func (h *perTurnHook) EventTypes() []string {
	return []string{"message.received"}
}

// PerTurnHook returns a pluginsdk.EventHook that can be registered on "message.received".
// When a received message contains memory signals, it extracts and stores memories.
func (e *Extractor) PerTurnHook() pluginsdk.EventHook {
	return &perTurnHook{extractor: e}
}

// postCompactHook implements pluginsdk.EventHook for post-compaction memory extraction.
type postCompactHook struct {
	extractor *Extractor
}

func (h *postCompactHook) Handle(ctx context.Context, event pluginsdk.Event) error {
	sessionID := event.SessionID
	if sessionID == "" {
		sessionID, _ = event.Data["session_id"].(string)
	}
	if sessionID == "" {
		return nil
	}

	tokensSaved, _ := event.Data["tokens_saved"].(int)

	// Fire-and-forget: don't block compaction flow.
	safego.Go(ctx, "memory.extractor.postCompact", func() {
		h.extractor.extractPostCompact(sessionID, tokensSaved)
	})
	return nil
}

func (h *postCompactHook) EventTypes() []string {
	return []string{"context.compacted"}
}

// PostCompactHook returns a pluginsdk.EventHook that can be registered on "context.compacted".
// After compaction, it extracts structured memories from the compacted content.
func (e *Extractor) PostCompactHook() pluginsdk.EventHook {
	return &postCompactHook{extractor: e}
}

// extractPerTurn performs lightweight per-turn extraction using the utility LLM.
func (e *Extractor) extractPerTurn(sessionID, content string) {
	if e.utilityCall == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Extract a memory from this user message. The message contains a preference, correction, or instruction that should be remembered for future sessions.

User message:
%s

Return a JSON object with these fields:
- "memory_key": short snake_case identifier (e.g. "prefers_terse_output", "use_sqlite_not_postgres")
- "summary": one-sentence summary of what to remember
- "body": fuller description if needed (empty string if summary is sufficient)
- "origin": one of "user", "feedback", "project", "reference" (use "user" for preferences, "feedback" for corrections)
- "confidence": 0.0-1.0 (how confident this is a real memory vs. conversational noise)
- "tags": array of 1-3 relevant tags

Return ONLY the JSON object, no markdown fences or explanation.`, truncateForPrompt(content, 1500))

	result, err := e.utilityCall(ctx, prompt)
	if err != nil {
		slog.Warn("memory: per-turn extraction LLM call failed", "err", err)
		return
	}

	var extracted struct {
		MemoryKey  string   `json:"memory_key"`
		Summary    string   `json:"summary"`
		Body       string   `json:"body"`
		Origin     string   `json:"origin"`
		Confidence float64  `json:"confidence"`
		Tags       []string `json:"tags"`
	}

	result = cleanJSONResponse(result)
	if err := json.Unmarshal([]byte(result), &extracted); err != nil {
		slog.Warn("memory: per-turn extraction parse failed", "err", err)
		return
	}

	// Skip low-confidence extractions.
	if extracted.Confidence < 0.5 {
		slog.Debug("memory: per-turn extraction skipped (low confidence)", "confidence", extracted.Confidence)
		return
	}

	if extracted.MemoryKey == "" || extracted.Summary == "" {
		slog.Debug("memory: per-turn extraction skipped (empty key or summary)")
		return
	}

	namespace := SessionNamespace(sessionID)
	if sessionID == "" {
		namespace = UserNamespace(e.defaultUser)
	}

	m := Memory{
		Namespace:  namespace,
		MemoryKey:  extracted.MemoryKey,
		Summary:    extracted.Summary,
		Body:       extracted.Body,
		Origin:     extracted.Origin,
		Trigger:    "per_turn",
		Confidence: extracted.Confidence,
		Tags:       extracted.Tags,
		SessionID:  sessionID,
	}

	if err := e.service.Store(context.Background(), m); err != nil {
		slog.Warn("memory: per-turn store failed", "err", err)
	}
}

// extractPostCompact performs rich structured extraction from compacted content.
func (e *Extractor) extractPostCompact(sessionID string, tokensSaved int) {
	if e.utilityCall == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`A chat session segment has just been compacted (approximately %d tokens compressed).

Extract structured memories from this session segment. Focus on:
- Decisions made (architecture, technology, approach)
- User preferences expressed (style, tools, workflows)
- Corrections given (things the user said were wrong)
- Facts learned (project structure, team info, constraints)

For each memory, return a JSON object with:
- "memory_key": short snake_case identifier
- "summary": one-sentence summary
- "body": fuller description (can be empty)
- "origin": one of "user", "feedback", "project", "reference"
- "confidence": 0.7-1.0 (post-compaction has more context, so higher confidence)
- "tags": array of 1-3 relevant tags

Return a JSON array of memory objects. If nothing notable was discussed, return an empty array [].
Return ONLY the JSON array, no markdown fences or explanation.

Session ID: %s`, tokensSaved, sessionID)

	result, err := e.utilityCall(ctx, prompt)
	if err != nil {
		slog.Warn("memory: post-compact extraction LLM call failed", "err", err)
		return
	}

	var extracted []struct {
		MemoryKey  string   `json:"memory_key"`
		Summary    string   `json:"summary"`
		Body       string   `json:"body"`
		Origin     string   `json:"origin"`
		Confidence float64  `json:"confidence"`
		Tags       []string `json:"tags"`
	}

	result = cleanJSONResponse(result)
	if err := json.Unmarshal([]byte(result), &extracted); err != nil {
		slog.Warn("memory: post-compact extraction parse failed", "err", err)
		return
	}

	namespace := SessionNamespace(sessionID)
	stored := 0

	for _, ex := range extracted {
		if ex.MemoryKey == "" || ex.Summary == "" {
			continue
		}
		if ex.Confidence < 0.5 {
			continue
		}

		m := Memory{
			Namespace:  namespace,
			MemoryKey:  ex.MemoryKey,
			Summary:    ex.Summary,
			Body:       ex.Body,
			Origin:     ex.Origin,
			Trigger:    "post_compact",
			Confidence: ex.Confidence,
			Tags:       ex.Tags,
			SessionID:  sessionID,
		}

		if err := e.service.Store(context.Background(), m); err != nil {
			slog.Warn("memory: post-compact store failed", "memory_key", ex.MemoryKey, "err", err)
			continue
		}
		stored++
	}

	slog.Info("memory: post-compact extraction stored",
		"stored", stored, "total", len(extracted), "session_id", sessionID)
}

// truncateForPrompt trims content to maxLen characters for inclusion in a prompt.
func truncateForPrompt(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "\n... [truncated]"
}

// cleanJSONResponse strips markdown code fences and trims whitespace from LLM JSON output.
func cleanJSONResponse(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json ... ``` fences.
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s[3:], "\n"); idx >= 0 {
			s = s[3+idx+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}
