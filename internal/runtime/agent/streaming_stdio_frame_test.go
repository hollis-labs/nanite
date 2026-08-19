package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestStreamingStdioUserFrame pins the c207/c208 fix (CW-20260516-0007).
// claude in `-p --input-format stream-json` mode parses every stdin line
// as JSON; the streaming-stdio runtime writes SendInput bytes verbatim.
// streamingStdioUserFrame must therefore emit exactly the NDJSON shape
// Anthropic's Streaming Input Mode accepts:
//
//	{"type":"user","message":{"role":"user","content":"<text>"}}
//
// This shape was verified by hand against claude v2.1.142 before the
// fix landed: piping that frame into `claude -p --input-format
// stream-json --output-format stream-json --verbose` produced a clean
// assistant/result stream. The test re-decodes the output and asserts
// the structure so a future refactor can't silently drift the shape.
func TestStreamingStdioUserFrame(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"plain", "say hi in 3 words"},
		{"empty", ""},
		{"multiline", "# Boot Prompt\n\nYou are a helpful agent.\nLine three."},
		{"quotes + backslash", `she said "hi" \ then left`},
		{"unicode + control", "café\ttab\nnewline"},
		{"json-looking", `{"type":"user"} not actually a frame`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := streamingStdioUserFrame(tc.text)
			if err != nil {
				t.Fatalf("streamingStdioUserFrame(%q): %v", tc.text, err)
			}

			// No trailing newline — the runtime's SendInput appends one.
			if len(raw) > 0 && raw[len(raw)-1] == '\n' {
				t.Errorf("frame ends with a raw newline; runtime appends its own: %q", raw)
			}

			// Decode and assert the exact shape claude consumes.
			var decoded struct {
				Type    string `json:"type"`
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("frame is not valid JSON: %v\nframe=%s", err, raw)
			}
			if decoded.Type != "user" {
				t.Errorf("type = %q, want \"user\"", decoded.Type)
			}
			if decoded.Message.Role != "user" {
				t.Errorf("message.role = %q, want \"user\"", decoded.Message.Role)
			}
			if decoded.Message.Content != tc.text {
				t.Errorf("message.content round-trip mismatch:\n got %q\nwant %q", decoded.Message.Content, tc.text)
			}

			// The whole frame must be a single line — claude's NDJSON
			// reader splits on '\n', so an un-escaped newline in the
			// serialized form would fracture one logical frame into two
			// parse attempts. json.Marshal escapes interior newlines to
			// \n, so the serialized bytes carry zero raw 0x0A.
			if strings.IndexByte(string(raw), '\n') != -1 {
				t.Errorf("serialized frame contains a raw newline — would fracture the NDJSON line: %q", raw)
			}
		})
	}
}

// TestBuildCLAUDEMD_SystemPromptSection pins CW-20260516-0007's
// system-prompt redirect: the resolved boot prompt is embedded into
// CLAUDE.md (claude auto-discovers it from cwd) rather than written to
// stdin. A non-empty systemPrompt must surface as an "## Operating
// Instructions" section; an empty one must leave the prior CLAUDE.md
// shape untouched.
func TestBuildCLAUDEMD_SystemPromptSection(t *testing.T) {
	t.Run("with system prompt", func(t *testing.T) {
		md := BuildCLAUDEMD("Test Agent", "a description", "You are an executor. Do the task end-to-end.")
		if !strings.Contains(md, "## Operating Instructions") {
			t.Error("CLAUDE.md missing the Operating Instructions section header")
		}
		if !strings.Contains(md, "You are an executor. Do the task end-to-end.") {
			t.Error("CLAUDE.md missing the system-prompt body")
		}
		// The envelope rules body must still be present.
		if !strings.Contains(md, "## Envelope Format") {
			t.Error("CLAUDE.md lost the envelope-rules body")
		}
		// Header still carries name + description.
		if !strings.Contains(md, "Test Agent") || !strings.Contains(md, "a description") {
			t.Error("CLAUDE.md lost the agent name/description header")
		}
	})

	t.Run("empty system prompt omits the section", func(t *testing.T) {
		md := BuildCLAUDEMD("Test Agent", "a description", "")
		if strings.Contains(md, "## Operating Instructions") {
			t.Error("empty systemPrompt should NOT emit an Operating Instructions section")
		}
		if !strings.Contains(md, "## Envelope Format") {
			t.Error("CLAUDE.md lost the envelope-rules body")
		}
	})

	t.Run("whitespace-only system prompt omits the section", func(t *testing.T) {
		md := BuildCLAUDEMD("Test Agent", "", "   \n\t  ")
		if strings.Contains(md, "## Operating Instructions") {
			t.Error("whitespace-only systemPrompt should NOT emit an Operating Instructions section")
		}
	})
}

// TestComposeBootdirParams_SystemPromptHonorsOverride pins that
// SetupParams.SystemPrompt — the value claudeLayout plants into
// CLAUDE.md — carries the AUTHORITATIVE boot prompt. CW-20260516-0007
// switched composeBootdirParams from bare composeSystemPrompt to
// resolveBootPrompt so a bootprofile-driven launch's catalog-authored
// BootPromptOverride reaches CLAUDE.md (previously only the raw stdin
// write — which c207/c208 proved unusable for streaming-stdio — saw
// the override).
func TestComposeBootdirParams_SystemPromptHonorsOverride(t *testing.T) {
	profile := &store.AgentProfile{
		ID:           "p1",
		Name:         "Test",
		Slug:         "test",
		SystemPrompt: "profile-level system prompt",
	}

	t.Run("override wins", func(t *testing.T) {
		opts := Options{
			Mode:               ModeLongLived,
			BootPromptOverride: "CATALOG-AUTHORED BOOT PROMPT",
		}
		_, params := composeBootdirParams(nil, opts, profile, "sess-1")
		want := "CATALOG-AUTHORED BOOT PROMPT\n\n" + mandatoryPostCompactionRereadInstruction
		if params.SystemPrompt != want {
			t.Errorf("SetupParams.SystemPrompt = %q, want %q (BootPromptOverride, bootprofile path, plus the mandatory post-compaction re-read instruction)", params.SystemPrompt, want)
		}
	})

	t.Run("no override falls back to composed prompt", func(t *testing.T) {
		opts := Options{Mode: ModeLongLived} // no override
		_, params := composeBootdirParams(nil, opts, profile, "sess-1")
		// resolveBootPrompt with empty override == composeSystemPrompt,
		// which includes the profile's own system prompt.
		if !strings.Contains(params.SystemPrompt, "profile-level system prompt") {
			t.Errorf("SetupParams.SystemPrompt = %q, want it to include the profile system prompt", params.SystemPrompt)
		}
	})
}

// TestResolveSystemPrompt pins the exported helper the chat service's
// mid-session CLAUDE.md regeneration uses (CW-20260516-0007 round 1).
// It must apply the same override-wins-else-compose rule as the
// unexported resolveBootPrompt — otherwise a slot regen would re-plant
// a different prompt than Boot did.
func TestResolveSystemPrompt(t *testing.T) {
	profile := &store.AgentProfile{
		ID:           "p1",
		Name:         "Test",
		SystemPrompt: "profile-level system prompt",
	}

	t.Run("override body wins verbatim, plus mandatory re-read appended", func(t *testing.T) {
		got := ResolveSystemPrompt("executor", profile, ModeLongLived, "CATALOG OVERRIDE")
		want := "CATALOG OVERRIDE\n\n" + mandatoryPostCompactionRereadInstruction
		if got != want {
			t.Errorf("ResolveSystemPrompt = %q, want %q", got, want)
		}
	})

	t.Run("empty override composes role + profile", func(t *testing.T) {
		got := ResolveSystemPrompt("executor", profile, ModeLongLived, "")
		if !strings.Contains(got, "profile-level system prompt") {
			t.Errorf("ResolveSystemPrompt = %q, want it to include the profile system prompt", got)
		}
		if !strings.Contains(got, "executor") {
			t.Errorf("ResolveSystemPrompt = %q, want it to include the executor role framing", got)
		}
		if !strings.Contains(got, mandatoryPostCompactionRereadInstruction) {
			t.Errorf("ResolveSystemPrompt = %q, want the mandatory post-compaction re-read instruction (Phase 2 task 03)", got)
		}
	})

	t.Run("parity with resolveBootPrompt", func(t *testing.T) {
		// The exported helper and the unexported Options-based hook must
		// produce identical output for the same inputs.
		opts := Options{Role: "reviewer", Mode: ModeLongLived}
		viaOpts := resolveBootPrompt(profile, opts)
		viaExport := ResolveSystemPrompt("reviewer", profile, ModeLongLived, "")
		if viaOpts != viaExport {
			t.Errorf("export drift:\n resolveBootPrompt   = %q\n ResolveSystemPrompt = %q", viaOpts, viaExport)
		}
	})
}
