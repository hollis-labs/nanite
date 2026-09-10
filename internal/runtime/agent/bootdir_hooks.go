package agent

// bootdir_hooks.go is the boot-dir hook plant target (CW-20260910-0015).
//
// # Why this exists
//
// Before this, Nanite could plant PROSE into a boot directory it owns but
// not a GATE. agent-setup's docs/gate-inventory.md §4 ranks the available
// interventions from measured experience: structural fixes work; gates
// work where they convert a judgement into a lookup and fire without
// being remembered; prose does not work. Nanite shipped only the third.
// The Tesseract decision nanite_plants_gates_not_only_prose is the
// ruling to close that gap.
//
// # Why NOT plant.Spec.Hooks
//
// go-agent-wrapper carries plant.Hook{Provider, Name, Payload}, and its
// SharedPlanter writes each payload to hooks/<provider>/<name> at 0700.
// Nanite does not populate that field, and plantSpec still rejects it.
// That is deliberate, and the reason is the whole design of this file:
//
// **A planted hook script does not fire on its own.** A hook runs
// because a settings file DECLARES it — an event key, an optional
// matcher, and a {type: command, command: ...} entry. Writing the payload
// and stopping there produces an executable file that nothing executes.
//
// The evidence, since this is a claim about another program's behavior
// and not something Nanite's tests can prove: agent-setup runs this exact
// hook set in production and declares every entry explicitly in
// profiles/base.md (SessionStart, PostCompact, PreToolUse with a matcher,
// PostToolUse, two Stop entries), naming each script by absolute path. If
// dropping a script into a directory were sufficient, none of that
// wiring would be needed. go-agent-wrapper's own plant.Spec.Hooks doc
// agrees the file drop is not the whole story — it says "encoding is
// provider-specific" and then only writes bytes, leaving the declaration
// to whoever knows the provider. A future reader who finds a
// hooks-directory convention should re-check this reasoning rather than
// assume it.
//
// plant.Hook cannot express the declaration: it has no event and no
// matcher, so it cannot drive the wiring. BootDirHook below carries both,
// and hook planting is therefore two halves that must ship together —
// the script (an artifact entry, executable, in a directory) and the
// wiring (merged into the provider's own settings document).
//
// # Soft by default
//
// Tesseract anti_rigidity_doctrine is standing and governs what may be
// built here: "Default to soft. A nudge or a hint, not a block. Reserve a
// hard, blocking gate for an actual security issue." A hook's disposition
// is carried by its exit code, which is the script's business, not this
// file's — this file plants whatever it is given. What it does NOT do is
// give Nanite an opinion: DefaultBootDirHooks is deliberately empty.
// Which hooks Nanite ships, if any, is CW-20260910-0016, and keeping the
// default empty is what stops a plumbing change from quietly shipping a
// policy.
//
// # Per-provider, and where the honesty line is
//
// Only claude gets wiring here. go-providers v0.26.0's capability matrix
// (provider/projection.go) reports FeatureHooks as SupportExplicit for
// claude and codex and SupportUnsupported for opencode — "explicit" means
// the provider is understood to have the feature but this package does
// not project it, leaving it to the caller.
//
// Nanite wires claude because its declaration shape is known and
// verifiable (the settings document, and agent-setup's working hook set
// against the same schema). Codex is NOT wired: the capability matrix
// asserts codex has hooks, but Nanite has no verified config shape for
// them, and this lane has already been burned twice by acting on an
// unverified architectural claim. Planting inert scripts for codex would
// be exactly that mistake. Opencode is not wired because its extension
// point is a JS plugin entry, a different mechanism entirely.
//
// Requesting hooks for an unwired provider is an error, not a silent
// no-op — a caller that asked for a gate and got nothing should be told.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/hollis-labs/agentkit/artifact"
)

// HookEvent is a harness lifecycle event a planted hook can bind to. The
// values are the settings-schema keys the CLI expects, not Nanite names.
type HookEvent string

const (
	HookSessionStart HookEvent = "SessionStart"
	HookPostCompact  HookEvent = "PostCompact"
	HookPreToolUse   HookEvent = "PreToolUse"
	HookPostToolUse  HookEvent = "PostToolUse"
	HookStop         HookEvent = "Stop"
)

func (e HookEvent) valid() bool {
	switch e {
	case HookSessionStart, HookPostCompact, HookPreToolUse, HookPostToolUse, HookStop:
		return true
	default:
		return false
	}
}

// matcherAllowed reports whether this event's declaration may carry a
// tool matcher. The tool events select which tool calls they fire on;
// the session-lifecycle events have nothing to match against, and a
// matcher on one is a caller mistake worth surfacing rather than
// silently dropping.
func (e HookEvent) matcherAllowed() bool {
	return e == HookPreToolUse || e == HookPostToolUse
}

// BootDirHook is one planted hook: a script, and the declaration that
// makes the harness run it.
//
// Two hooks on the SAME event are two separate declarations, never
// merged. That is load-bearing rather than incidental — agent-setup runs
// stop-disposition.sh and context-report.sh as two Stop entries because
// "that hook speaks to the agent at exit 2 and this one reports to the
// operator at exit 0. Same event, opposite audiences." Folding them into
// one entry would make one of them wrong.
type BootDirHook struct {
	// Event is the lifecycle event this hook binds to.
	Event HookEvent
	// Matcher is the tool-name pattern for PreToolUse/PostToolUse.
	// Empty means "every tool call". Must be empty for the other events.
	Matcher string
	// Name is the planted script's file name (e.g. "stop-disposition.sh").
	// A bare name: no separators, no traversal.
	Name string
	// Script is the executable body planted at that name.
	Script []byte
}

// DefaultBootDirHooks is the hook set Nanite plants when a caller does
// not supply one. EMPTY, and that is now a decided policy rather than a
// placeholder: CW-20260910-0016, ruled by Chrispian 2026-09-09 —
// **Nanite plants no hooks by default and exposes the mechanism.**
//
// Do not fill this in without revisiting that ruling. Four independent
// grounds, any one of which is sufficient:
//
//  1. Nanite is not a personal catalog. The candidate gate set
//     (agent-setup's docs/gate-inventory.md §2) is five gates aimed at
//     one operator's workflow, and three of the five are unusable without
//     Torque — which Nanite does not know its user has. A hook planted
//     here fires for EVERY agent Nanite boots, including ones imported
//     for someone else's purposes.
//
//  2. **Boot dirs are headless, and this is the ground that generalizes.**
//     CLI dispatch spawns with no TTY — which is exactly why
//     bootdir_provider_config.go plants acceptEdits for claude and
//     approval_policy=never for codex, a config that can surface an
//     approval prompt DEADLOCKS (the headless-codex hang was a live bug).
//     So a hard gate here is not a guardrail, it is that hang again; and
//     a soft gate that "presents and proceeds" presents to nobody. The
//     only shape that survives is one speaking to the AGENT at exit 2
//     rather than to an operator.
//
//  3. Only claude has verified wiring (see this file's header). A default
//     set would fire for one provider in three while reading as coverage.
//
//  4. Tesseract anti_rigidity_doctrine (hard gates only for a genuine
//     security issue) and audit_mitigations_nudge_only_zero_new_gates
//     (31 mitigations, not one a new gate). None of the five carries a
//     security justification; they are workflow discipline.
//
// The gates themselves are not wrong — they belong in the personal
// catalog that owns that workflow, where they already work. What Nanite
// owes is the mechanism, which is this file, reachable through
// SetupParams.Hooks.
var DefaultBootDirHooks []BootDirHook

// hookScriptMode is the mode for a planted hook script. It must be
// executable or the harness cannot run it, and it is the one place in
// the boot dir where the executable bit is load-bearing.
const hookScriptMode fs.FileMode = 0o700

// hookDirMode matches hookScriptMode: the directory holding executable
// scripts is owner-only, consistent with the boot dir's own 0700.
const hookDirMode fs.FileMode = 0o700

// hookOwnershipGroup keeps planted hooks in their own materialization
// ownership group, distinct from the general boot-dir group, so a future
// caller can reconcile the hook set alone via materialize.Selection
// without touching the rest of the planted tree.
const hookOwnershipGroup = "nanite:hooks"

// hookDirRelPath is the bootdir-relative directory a provider's hook
// scripts are planted into. Matches go-agent-wrapper's own
// hooks/<provider>/<name> convention, so a hook planted here lands where
// the wrapper's legacy Hooks conversion would have put it even though
// Nanite does not route through that conversion.
func hookDirRelPath(provider string) string {
	return path.Join("hooks", strings.ToLower(provider))
}

// validateHooks rejects a hook set that could not be planted or declared
// correctly, before any of it is written.
func validateHooks(hooks []BootDirHook) error {
	seen := make(map[string]bool, len(hooks))
	for i, h := range hooks {
		if !h.Event.valid() {
			return fmt.Errorf("agent: bootdir hook %d: unknown event %q", i, h.Event)
		}
		name := strings.TrimSpace(h.Name)
		if name == "" {
			return fmt.Errorf("agent: bootdir hook %d: name is required", i)
		}
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return fmt.Errorf("agent: bootdir hook %q: name must be a bare file name", h.Name)
		}
		if h.Matcher != "" && !h.Event.matcherAllowed() {
			return fmt.Errorf("agent: bootdir hook %q: event %s takes no matcher", h.Name, h.Event)
		}
		if len(h.Script) == 0 {
			return fmt.Errorf("agent: bootdir hook %q: script is empty", h.Name)
		}
		if seen[name] {
			return fmt.Errorf("agent: bootdir hook %q: duplicate name", h.Name)
		}
		seen[name] = true
	}
	return nil
}

// hookArtifactEntries renders a hook set as artifact entries: the
// provider's hook directory, then one executable script per hook.
//
// Returns nil for an empty set so a no-hook boot plants nothing at all —
// not an empty hooks/ directory that would suggest a mechanism is armed
// when it is not.
func hookArtifactEntries(provider string, hooks []BootDirHook) ([]artifact.Entry, error) {
	if len(hooks) == 0 {
		return nil, nil
	}
	if err := validateHooks(hooks); err != nil {
		return nil, err
	}
	dir := hookDirRelPath(provider)
	entries := []artifact.Entry{{
		Path: dir,
		Kind: artifact.EntryDirectory,
		Mode: hookDirMode,
		Ownership: artifact.Ownership{
			EntryID: "nanite:hookdir:" + provider,
			GroupID: hookOwnershipGroup,
		},
	}}
	for _, h := range hooks {
		body := make([]byte, len(h.Script))
		copy(body, h.Script)
		entries = append(entries, artifact.Entry{
			Path:  path.Join(dir, h.Name),
			Kind:  artifact.EntryFile,
			Mode:  hookScriptMode,
			Bytes: body,
			Ownership: artifact.Ownership{
				EntryID: "nanite:hook:" + provider + ":" + h.Name,
				GroupID: hookOwnershipGroup,
			},
		})
	}
	return entries, nil
}

// claudeHookSettings renders a hook set as the "hooks" value of claude's
// settings document.
//
// Commands are ABSOLUTE, built from bootDir. The alternative — a relative
// path resolved against the harness's working directory — depends on cwd
// semantics Nanite would be guessing at, and a hook that silently fails
// to resolve is worse than no hook: it looks armed and does nothing.
// bootDir is known at plant time, and settings.json is planted into that
// same ephemeral directory, so an absolute path is both unambiguous and
// no less portable.
//
// Shape, matching the settings schema:
//
//	"hooks": {
//	  "Stop": [ {"hooks": [{"type": "command", "command": "/abs/path"}]} ],
//	  "PreToolUse": [ {"matcher": "Bash", "hooks": [...]} ]
//	}
//
// Order within an event follows the caller's slice, so two hooks on one
// event stay two entries in the order given.
func claudeHookSettings(bootDir string, hooks []BootDirHook) (map[string]any, error) {
	if len(hooks) == 0 {
		return nil, nil
	}
	if err := validateHooks(hooks); err != nil {
		return nil, err
	}
	dir := hookDirRelPath("claude")
	out := map[string]any{}
	for _, h := range hooks {
		entry := map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": path.Join(bootDir, dir, h.Name),
			}},
		}
		if h.Matcher != "" {
			entry["matcher"] = h.Matcher
		}
		key := string(h.Event)
		existing, _ := out[key].([]any)
		out[key] = append(existing, entry)
	}
	return out, nil
}

// mergeClaudeSettingsHooks folds a hooks value into claude's settings
// document and encodes it the way go-providers encodes the planted file.
//
// go-providers' ClaudeAdapter.SettingsDocument exists precisely for this:
// its own doc says "a document because merging is the point — the
// permissions.allow / deny policy this package deliberately leaves to
// apps is added by the caller," and specifies that MarshalIndent with two
// spaces plus a trailing newline reproduces the planted file byte for
// byte. Adding "hooks" is that sanctioned extension, not a reach across
// the boundary bootdir_provider_config.go draws: go-providers owns the
// document's schema and still renders every key it owns.
//
// A nil or empty hooks value returns the document unchanged, so a
// no-hook boot plants the identical bytes it planted before this file
// existed.
func mergeClaudeSettingsHooks(doc map[string]any, hooks map[string]any) (string, error) {
	if len(hooks) > 0 {
		doc["hooks"] = hooks
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("agent: encode claude settings: %w", err)
	}
	return string(encoded) + "\n", nil
}

// hooksUnsupportedError is the error a provider without verified hook
// wiring returns when a caller asks it to plant hooks. Deliberately an
// error and not a silent no-op: a caller that asked for a gate and got
// nothing should be told, and an inert planted script is the specific
// failure this file exists to avoid.
func hooksUnsupportedError(provider, reason string) error {
	return fmt.Errorf("agent: bootdir hooks for provider %q are not supported: %s", provider, reason)
}
