package selftools

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/agent/subagent"
)

// TestCallSpawnSubagent_FailFast_NoProfile_EmitsConfigEnvelope is the
// MCP-layer regression test for CW-20260519-0123 c271 pattern. The LLM
// supplies a role name that has no registered profile (e.g.
// "system-architect"). Before the fail-fast gate landed, this fell
// through to the orphan reaper after 60s with "timeout: orphan, no
// child session" and the parent saw `context deadline exceeded`.
//
// Post-fix: the spawn envelope reports success=false with
// error.kind=config and error.message naming the unresolved role, in
// the same turn. No orphan row, no timer, no 60s wait.
func TestCallSpawnSubagent_FailFast_NoProfile_EmitsConfigEnvelope(t *testing.T) {
	st := newSubagentTestTransport(t, subagent.EchoRunner{})
	// Wire an explicit private profile reader. The host no longer seeds
	// historical profiles; "system-architect" is deliberately unregistered.
	st.Subagent.SetProfileResolver(privateFailFastProfiles{})

	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-1",
		"parent_agent_id":   "primary",
		"role":              "system-architect",
		"prompt":            "design the dispatch path",
		"mode":              subagent.ModeSync,
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true on unregistered-role spawn")
	}
	env := parseEnvelopeFromResult(t, res)
	if env.Success {
		t.Fatalf("expected success=false on unregistered-role spawn: %+v", env)
	}
	if env.Error == nil {
		t.Fatal("expected error block on unregistered-role envelope")
	}
	if env.Error.Kind != subagent.ErrorKindConfig {
		t.Errorf("error.kind = %q, want %q (no-profile is a config fault, not internal)",
			env.Error.Kind, subagent.ErrorKindConfig)
	}
	if !strings.Contains(env.Error.Message, "system-architect") {
		t.Errorf("error.message = %q; expected unresolved role name in body", env.Error.Message)
	}
	if env.Error.Context == nil || env.Error.Context["role"] != "system-architect" {
		t.Errorf("error.context.role = %v; expected role echoed for parent retry", env.Error.Context)
	}
	// PR #214 review fix item 5: error.context.reason carries the
	// stable ConfigReason* discriminator so downstream consumers branch
	// without parsing the message string.
	if env.Error.Context == nil || env.Error.Context["reason"] != subagent.ConfigReasonNoProfile {
		t.Errorf("error.context.reason = %v; want %q", env.Error.Context["reason"], subagent.ConfigReasonNoProfile)
	}
}

// TestCallSpawnSubagent_FailFast_NotExecutable_EmitsConfigEnvelope is
// the MCP-layer regression test for CW-20260519-0123 c256 pattern. The
// LLM dispatches the live `planner` profile, which is registered but
// has can_execute=false (the planner profile is decomposition prompt
// only — Phase-6 tool surface deferred). Before this gate, the runner
// drove a chat turn and drainCapture hung 300s with zero output.
//
// Post-fix: spawn envelope reports success=false with
// error.kind=config in the same turn. The planner case is the
// canonical "fail fast instead of stall" the ticket retires.
func TestCallSpawnSubagent_FailFast_NotExecutable_EmitsConfigEnvelope(t *testing.T) {
	st := newSubagentTestTransport(t, subagent.EchoRunner{})
	st.Subagent.SetProfileResolver(privateFailFastProfiles{})

	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-1",
		"parent_agent_id":   "primary",
		"role":              "planner",
		"prompt":            "plan the migration",
		"mode":              subagent.ModeSync,
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true on non-executable-role spawn")
	}
	env := parseEnvelopeFromResult(t, res)
	if env.Success {
		t.Fatalf("expected success=false on non-executable-role spawn: %+v", env)
	}
	if env.Error == nil {
		t.Fatal("expected error block on non-executable-role envelope")
	}
	if env.Error.Kind != subagent.ErrorKindConfig {
		t.Errorf("error.kind = %q, want %q", env.Error.Kind, subagent.ErrorKindConfig)
	}
	// PR #214 review fix item 5: not-executable maps to a distinct
	// ConfigReason discriminator from no-profile so the parent can
	// branch (e.g. "register tool surface" vs "register the profile").
	if env.Error.Context == nil || env.Error.Context["reason"] != subagent.ConfigReasonNotExecutable {
		t.Errorf("error.context.reason = %v; want %q", env.Error.Context["reason"], subagent.ConfigReasonNotExecutable)
	}
}

// TestCallSpawnSubagent_FailFast_TextOnlyWhitelist_Admitted ensures
// hint-selector (the canonical text-only whitelist member) is admitted
// through the gate. Without this exemption, the F5 think-block v2 hint
// dispatch (which dispatches hint-selector via the dispatch.Spawner
// surface) would break. EchoRunner returns a non-empty summary so the
// sync path doesn't trip ErrorKindEmptyReply for unrelated reasons.
func TestCallSpawnSubagent_FailFast_TextOnlyWhitelist_Admitted(t *testing.T) {
	st := newSubagentTestTransport(t, subagent.EchoRunner{})
	st.Subagent.SetProfileResolver(privateFailFastProfiles{})

	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-hint",
		"parent_agent_id":   "_system_",
		"role":              "hint-selector",
		"prompt":            `{"user_input":"plan","scope_tier":"open"}`,
		"mode":              subagent.ModeSync,
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}
	env := parseEnvelopeFromResult(t, res)
	// hint-selector should NOT be rejected by the config gate. The
	// EchoRunner produces text, so success=true is the expected shape;
	// the assertion here is the negative one: NOT a config rejection.
	if env.Error != nil && env.Error.Kind == subagent.ErrorKindConfig {
		t.Fatalf("hint-selector rejected by config gate despite text-only whitelist: %+v", env.Error)
	}
}

// privateFailFastProfiles exercises the standalone profile gate without
// seeding historical profiles or claiming host actor admission.
type privateFailFastProfiles struct{}

func (privateFailFastProfiles) GetAgentBySlug(_ context.Context, slug string) (*subagent.Profile, error) {
	switch slug {
	case "planner", "hint-selector":
		return &subagent.Profile{ID: "private-" + slug, Slug: slug, CanExecute: false}, nil
	default:
		return nil, sql.ErrNoRows
	}
}
