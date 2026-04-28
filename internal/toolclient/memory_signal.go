package toolclient

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/hollis-labs/nanite/internal/memory"
)

// MemoryRecaller is the narrow surface the broker uses to query the embedded
// Conduit memory store for prior successful tool sequences. The interface
// exists so tests can inject a deterministic mock without standing up a
// real Conduit instance.
//
// The production implementation is a thin adapter around *memory.Service
// (see NewMemoryRecaller). When the broker is constructed without a
// MemoryRecaller — production deployments that don't have Conduit wired,
// or tests that don't care — every Recall call returns empty and the
// broker falls through to keyword + skill ranking.
type MemoryRecaller interface {
	// RecallToolPatterns returns memory-derived ranking hints for the given
	// intent. Empty result + nil error is the "no signal" path; a real error
	// (Conduit unreachable, etc.) is logged but never gates selection.
	RecallToolPatterns(ctx context.Context, intent string) ([]ToolPatternHit, error)

	// RecordToolPattern persists a successful tool sequence so future
	// intents on the same theme can recall it. Best-effort; failures are
	// logged but never bubble up.
	RecordToolPattern(ctx context.Context, sessionID, intent string, sequence []string, outcome string) error
}

// ToolPatternHit is a memory-recall hit: the broker should weight these
// tool names higher because the same agent succeeded with them on a
// similar intent in a prior session.
type ToolPatternHit struct {
	ToolName   string
	Confidence float64 // 0..1; from the underlying revision
	Source     string  // memory_key, for diagnostics
}

// DefaultMemoryWeight is the rank contribution per memory-recall hit. Sits
// below operator-skill weight (5) and above keyword match (1-2). See
// ranking.go for the full precedence table.
const DefaultMemoryWeight = 3

// ToolPatternsNamespace returns the Conduit namespace under which the
// broker writes/reads tool-use patterns.
//
// The convention is `user/<user>/project/nanite/memory/tool_use_patterns`.
// We deliberately choose `user/default` for the default user — this matches
// the existing memory.UserNamespace / ProjectNamespace conventions in
// internal/memory/service.go. Operators that override the user slug at
// deployment time can pass a custom namespace via NewMemoryRecallerNS.
func ToolPatternsNamespace(user string) string {
	if user == "" {
		user = "default"
	}
	return fmt.Sprintf("user/%s/project/nanite/memory/tool_use_patterns", user)
}

// memoryRecaller is the production implementation of MemoryRecaller. It
// wraps *memory.Service so callers don't have to construct Conduit RecallOpts
// inline at every selection pass.
type memoryRecaller struct {
	svc       *memory.Service
	namespace string
}

// NewMemoryRecaller returns a MemoryRecaller backed by the given memory
// service. A nil service yields a no-op recaller (RecallToolPatterns
// returns nil; RecordToolPattern is a no-op) so wiring code can pass the
// recaller unconditionally.
func NewMemoryRecaller(svc *memory.Service) MemoryRecaller {
	return &memoryRecaller{svc: svc, namespace: ToolPatternsNamespace("default")}
}

// NewMemoryRecallerNS is like NewMemoryRecaller but with an explicit
// namespace — used by deployments where the user slug differs from the
// default convention.
func NewMemoryRecallerNS(svc *memory.Service, namespace string) MemoryRecaller {
	if namespace == "" {
		namespace = ToolPatternsNamespace("default")
	}
	return &memoryRecaller{svc: svc, namespace: namespace}
}

// RecallToolPatterns implements MemoryRecaller.
func (r *memoryRecaller) RecallToolPatterns(ctx context.Context, intent string) ([]ToolPatternHit, error) {
	if r == nil || r.svc == nil {
		return nil, nil
	}
	if intent == "" || intent == "*" {
		return nil, nil
	}

	memories, err := r.svc.Recall(ctx, memory.RecallOpts{
		Namespaces: []string{r.namespace},
		Ranking:    "relevance",
		Query:      intent,
		Limit:      8,
		Tags:       []string{"tool_use_pattern"},
	})
	if err != nil {
		// Don't gate selection on memory unreachability.
		slog.Warn("toolclient: memory recall failed; broker falling through to keyword+skills",
			"intent", intent, "err", err)
		return nil, nil
	}

	var hits []ToolPatternHit
	for _, m := range memories {
		// The summary stores the tool sequence as a comma-separated list;
		// the body holds free prose. We extract names from the summary
		// because that's the canonical format RecordToolPattern writes.
		for _, name := range parseToolNames(m.Summary) {
			hits = append(hits, ToolPatternHit{
				ToolName:   name,
				Confidence: m.Confidence,
				Source:     m.MemoryKey,
			})
		}
	}
	return hits, nil
}

// RecordToolPattern implements MemoryRecaller.
func (r *memoryRecaller) RecordToolPattern(ctx context.Context, sessionID, intent string, sequence []string, outcome string) error {
	if r == nil || r.svc == nil {
		return nil
	}
	if len(sequence) == 0 {
		return nil
	}
	if intent == "" {
		intent = "general"
	}

	// Vanta memory keys are normalized: a-z 0-9 _ per segment. The slug here
	// keys the pattern to the intent so future RecallToolPatterns calls on
	// the same theme can dedup. Hyphens become underscores; everything else
	// non-alphanumeric becomes underscore.
	memoryKey := "tool_pattern_" + slugifyForVanta(intent)

	body := fmt.Sprintf("Intent: %s\nOutcome: %s\nSequence: %s",
		intent, outcome, strings.Join(sequence, " -> "))

	err := r.svc.Store(ctx, memory.Memory{
		Namespace:  r.namespace,
		MemoryKey:  memoryKey,
		Summary:    strings.Join(sequence, ", "),
		Body:       body,
		Origin:     "observation",
		Trigger:    "post_compact",
		Confidence: 0.7,
		Tags:       []string{"tool_use_pattern", "broker_d3"},
		SessionID:  sessionID,
	})
	if err != nil {
		slog.Warn("toolclient: failed to record tool pattern to memory",
			"intent", intent, "err", err)
		return err
	}
	slog.Info("toolclient: recorded tool pattern to memory",
		"intent", intent, "sequence", sequence, "outcome", outcome,
		"namespace", r.namespace, "memory_key", memoryKey)
	return nil
}

// parseToolNames parses a comma-separated tool name list, stripping
// whitespace. Names are validated by the simple convention used everywhere
// post ADR-002: alphanumeric + underscore.
func parseToolNames(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name == "" {
			continue
		}
		if !validToolName.MatchString(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

var validToolName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// slugifyForVanta normalizes a free-form string into a Vanta-key-compatible
// slug: lowercase, hyphens to underscores, non-alphanumeric to underscore,
// run of underscores collapsed.
func slugifyForVanta(s string) string {
	lower := strings.ToLower(s)
	var b strings.Builder
	prevUnderscore := false
	for _, r := range lower {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteRune('_')
				prevUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "general"
	}
	// Cap at 50 chars so memory keys stay readable.
	if len(out) > 50 {
		out = out[:50]
		out = strings.TrimRight(out, "_")
	}
	return out
}
