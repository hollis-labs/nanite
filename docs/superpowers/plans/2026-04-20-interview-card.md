# InterviewCard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `QuestionForm` with a polished `InterviewCard` component that renders agent questions in the TodoItem visual language, with card/compact display modes, Accept suggested, Dismiss, and a backend persistence fix so submitted cards show their confirmation state after refresh.

**Architecture:** `InterviewCard` renders via the `EnvelopeRenderer` fallback path (same as `QuestionForm` today), receiving the full `Envelope` struct. The Go and TypeScript `Envelope`/`Question` types gain new optional fields (`title`, `subtitle`, `description`, `displayStyle`, `prior_response`). The backend API injects `prior_response` into envelope JSON after fetching session messages, so the card derives its submitted state from a prop — not `useState`.

**Tech Stack:** Go (backend types + API enrichment), React 19 + TypeScript + Tailwind CSS (frontend component). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-04-20-interview-card-design.md`

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `internal/chat/envelope.go` | Modify | Add `Title`, `Subtitle` to `Envelope`; `Description`, `DisplayStyle` to `Question` |
| `internal/api/envelopes.go` | Modify | Add `injectEnvelopePriorResponses` helper |
| `internal/api/sessions.go` | Modify | Call `injectEnvelopePriorResponses` after each message list call |
| `internal/api/envelopes_test.go` (create) | Create | Go test for `injectEnvelopePriorResponses` |
| `ui/src/lib/types.ts` | Modify | Extend `Envelope` and `Question` TypeScript interfaces |
| `ui/src/components/chat/envelopes/InterviewCard.tsx` | Create | New component — full replacement for `QuestionForm` |
| `ui/src/components/chat/envelopes/QuestionForm.tsx` | Delete | Retired |
| `ui/src/components/chat/envelopes/EnvelopeRenderer.tsx` | Modify | Swap `QuestionForm` → `InterviewCard`; pass `userMessageCount` |
| `ui/src/components/chat/ChatTranscript.tsx` | Modify | Compute + pass `userMessageCount` to `EnvelopeRenderer` |
| `config/envelopes.yaml` | Modify | Remove `component`/`export` from `question-form` (backend-only) |

---

### Task 1: Extend Go Envelope and Question types

**Files:**
- Modify: `internal/chat/envelope.go`

- [ ] **Step 1: Add new fields to the Go structs**

In `internal/chat/envelope.go`, update `Envelope` and `Question`:

```go
type Envelope struct {
	Kind      string         `json:"kind"`
	Version   int            `json:"version"`
	Type      string         `json:"type"`
	Title     string         `json:"title,omitempty"`
	Subtitle  string         `json:"subtitle,omitempty"`
	Proposals []Proposal     `json:"proposals,omitempty"`
	Questions []Question     `json:"questions,omitempty"`
	Status    *Status        `json:"status,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type Question struct {
	Prompt       string   `json:"prompt"`
	Type         string   `json:"type"`
	Options      []string `json:"options,omitempty"`
	Required     bool     `json:"required"`
	Default      string   `json:"default,omitempty"`
	Description  string   `json:"description,omitempty"`
	DisplayStyle string   `json:"display_style,omitempty"`
}
```

- [ ] **Step 2: Verify Go compiles clean**

```bash
cd ~/Projects-apps/nanite && go build ./...
```

Expected: no output (success).

- [ ] **Step 3: Run existing envelope tests**

```bash
go test ./internal/chat/... -run TestEnvelope -v
```

Expected: all pass. If `TestDetectStuckLoop` fires, re-run once — it's a pre-existing flake.

- [ ] **Step 4: Commit**

```bash
git add internal/chat/envelope.go
git commit -m "feat(envelope): add title/subtitle to Envelope and description/displayStyle to Question"
```

---

### Task 2: Backend — inject prior_response into served message envelopes

**Files:**
- Modify: `internal/api/envelopes.go`
- Modify: `internal/api/sessions.go`
- Create: `internal/api/envelopes_inject_test.go`

**Background:** When session messages are served to the frontend, each message's `Envelope` field is raw JSON. If that envelope was already responded to, the frontend needs `prior_response` in the envelope JSON so `InterviewCard` shows the confirmation state on load instead of the live form.

- [ ] **Step 1: Write the failing test**

Create `internal/api/envelopes_inject_test.go`:

```go
package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestInjectEnvelopePriorResponses(t *testing.T) {
	// A message whose envelope has an id that was already responded to.
	respondedAt := time.Now().UTC()
	inst := &store.EnvelopeInstance{
		ID:           "env-abc",
		EnvelopeJSON: `{"kind":"envelope","version":1,"type":"question-form","id":"env-abc"}`,
		RespondedAt:  &respondedAt,
		ResponseJSON: `{"v":1,"kind":"question-form","id":"env-abc","status":"submitted"}`,
	}

	messages := []store.Message{
		{Envelope: `{"kind":"envelope","version":1,"type":"question-form","id":"env-abc"}`},
		{Envelope: `{"kind":"envelope","version":1,"type":"question-form","id":"env-xyz"}`}, // no response
		{Envelope: ``}, // no envelope
	}

	lookup := map[string]*store.EnvelopeInstance{
		"env-abc": inst,
	}

	result := injectEnvelopePriorResponses(messages, lookup)

	// First message: must have prior_response injected.
	var env0 map[string]any
	if err := json.Unmarshal([]byte(result[0].Envelope), &env0); err != nil {
		t.Fatalf("failed to parse enriched envelope 0: %v", err)
	}
	if env0["prior_response"] == nil {
		t.Errorf("expected prior_response in first message envelope, got nil")
	}
	pr, ok := env0["prior_response"].(map[string]any)
	if !ok {
		t.Fatalf("prior_response is not an object")
	}
	if pr["status"] != "submitted" {
		t.Errorf("prior_response.status: got %v, want submitted", pr["status"])
	}

	// Second message: no prior_response (no response stored for env-xyz).
	var env1 map[string]any
	if err := json.Unmarshal([]byte(result[1].Envelope), &env1); err != nil {
		t.Fatalf("failed to parse envelope 1: %v", err)
	}
	if env1["prior_response"] != nil {
		t.Errorf("envelope 1 should not have prior_response, got %v", env1["prior_response"])
	}

	// Third message: empty envelope stays empty.
	if result[2].Envelope != `` {
		t.Errorf("empty envelope should be unchanged, got %q", result[2].Envelope)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/api/... -run TestInjectEnvelopePriorResponses -v
```

Expected: FAIL — `injectEnvelopePriorResponses` is not defined yet.

- [ ] **Step 3: Implement `injectEnvelopePriorResponses`**

Add to `internal/api/envelopes.go`:

```go
// injectEnvelopePriorResponses post-processes messages returned from the store,
// injecting a "prior_response" key into the envelope JSON of any message whose
// envelope was already responded to. The lookup map is keyed by envelope ID.
// Messages without an envelope or with an envelope missing an "id" field are
// returned unchanged.
func injectEnvelopePriorResponses(messages []store.Message, lookup map[string]*store.EnvelopeInstance) []store.Message {
	for i, msg := range messages {
		if msg.Envelope == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(msg.Envelope), &env); err != nil {
			continue
		}
		id, _ := env["id"].(string)
		if id == "" {
			continue
		}
		inst, ok := lookup[id]
		if !ok || inst.ResponseJSON == "" {
			continue
		}
		var resp any
		if err := json.Unmarshal([]byte(inst.ResponseJSON), &resp); err != nil {
			continue
		}
		env["prior_response"] = resp
		enriched, err := json.Marshal(env)
		if err != nil {
			continue
		}
		messages[i].Envelope = string(enriched)
	}
	return messages
}
```

Add the required import to the file's import block: `"encoding/json"` (add if not already present).

- [ ] **Step 4: Add `buildEnvelopeLookup` helper**

Also add to `internal/api/envelopes.go`:

```go
// buildEnvelopeLookup fetches EnvelopeInstances for all envelope IDs found in
// the given messages and returns them keyed by envelope ID.
func buildEnvelopeLookup(s *store.Store, messages []store.Message) map[string]*store.EnvelopeInstance {
	lookup := make(map[string]*store.EnvelopeInstance)
	for _, msg := range messages {
		if msg.Envelope == "" {
			continue
		}
		var env struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(msg.Envelope), &env); err != nil || env.ID == "" {
			continue
		}
		if _, seen := lookup[env.ID]; seen {
			continue
		}
		inst, err := s.GetEnvelopeInstance(env.ID)
		if err != nil {
			continue // not found or error — skip silently
		}
		lookup[env.ID] = inst
	}
	return lookup
}
```

- [ ] **Step 5: Call enrichment in message list handlers**

In `internal/api/sessions.go`, find the three message list sites and enrich after each fetch.

In `handleGetSession` (around line 118):
```go
messages, err := a.Services.Store.ListMessages(id, 50)
if err != nil {
    a.errorResp(w, http.StatusInternalServerError, err.Error())
    return
}
lookup := buildEnvelopeLookup(a.Services.Store, messages)
messages = injectEnvelopePriorResponses(messages, lookup)
```

In `handleListSessionMessages` around the `around` branch (line 416):
```go
page, err := a.Services.Store.ListMessagesAroundID(sessionID, around, before, after)
if err != nil {
    a.errorResp(w, http.StatusInternalServerError, err.Error())
    return
}
lookup := buildEnvelopeLookup(a.Services.Store, page.Messages)
page.Messages = injectEnvelopePriorResponses(page.Messages, lookup)
a.jsonResp(w, http.StatusOK, page)
```

In `handleListSessionMessages` for the paginated branch (line 433):
```go
page, err := a.Services.Store.ListMessagesPaginated(sessionID, limit, offset)
if err != nil {
    a.errorResp(w, http.StatusInternalServerError, err.Error())
    return
}
lookup := buildEnvelopeLookup(a.Services.Store, page.Messages)
page.Messages = injectEnvelopePriorResponses(page.Messages, lookup)
a.jsonResp(w, http.StatusOK, page)
```

Note: `MessagePage` has a `Messages` field (capital M) — check the exact field name in `store/sessions.go:295` before writing. It's `Messages []Message`.

- [ ] **Step 6: Run test to verify it passes**

```bash
go test ./internal/api/... -run TestInjectEnvelopePriorResponses -v
```

Expected: PASS.

- [ ] **Step 7: Run full backend test suite**

```bash
go test ./... 2>&1 | grep -E "FAIL|ok|---"
```

Expected: all packages pass. Re-run once if `TestDetectStuckLoop` fires.

- [ ] **Step 8: Commit**

```bash
git add internal/api/envelopes.go internal/api/envelopes_inject_test.go internal/api/sessions.go
git commit -m "feat(api): inject prior_response into envelope JSON for responded envelopes"
```

---

### Task 3: Extend TypeScript types

**Files:**
- Modify: `ui/src/lib/types.ts`

- [ ] **Step 1: Extend `Envelope` and `Question` interfaces**

In `ui/src/lib/types.ts`, find `Envelope` (line 469) and `Question` (line 494) and update:

```typescript
export interface Envelope {
  kind: string;
  version: number;
  type: string;
  id?: string;
  title?: string;                          // NEW — agent-defined card title
  subtitle?: string;                       // NEW — agent-defined subheading
  prior_response?: ResponseV1;             // NEW — set by backend if already answered
  proposals?: Proposal[];
  questions?: Question[];
  approval?: EnvelopeApprovalRequest;
  status?: { phase: string; progress: number };
  data?: Record<string, unknown>;
}

export interface Question {
  prompt: string;
  type: "text" | "textarea" | "select" | "radio" | "checkbox";
  options?: (string | { value: string; label: string; description?: string })[];
  required: boolean;
  default?: string;
  description?: string;                    // NEW — paragraph shown in card display
  displayStyle?: "compact" | "card";       // NEW — defaults to "compact"
}
```

`ResponseV1` is imported from `@/lib/envelope-response` — add the import at the top of `types.ts` if not already present:

```typescript
import type { ResponseV1 } from "@/lib/envelope-response";
```

Note: check if `types.ts` already imports from `envelope-response`; if so, add `ResponseV1` to the existing import.

- [ ] **Step 2: Run TypeScript check**

```bash
cd ~/Projects-apps/nanite/ui && npx tsc -b --noEmit 2>&1 | head -30
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
cd ~/Projects-apps/nanite
git add ui/src/lib/types.ts
git commit -m "feat(types): extend Envelope and Question with InterviewCard fields"
```

---

### Task 4: Build InterviewCard — skeleton, persistence check, and confirmation state

**Files:**
- Create: `ui/src/components/chat/envelopes/InterviewCard.tsx`

This task builds the outer shell — the part that determines whether to show the live form or the confirmation row based on `prior_response`.

- [ ] **Step 1: Create the skeleton**

Create `ui/src/components/chat/envelopes/InterviewCard.tsx`:

```typescript
import { useState, useRef, useEffect } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { type Answer, ResponseStatus } from "@/lib/envelope-response";
import type { Envelope, Question } from "@/lib/types";
import type { EnvelopeResponder } from "./EnvelopeRenderer";

interface InterviewCardProps {
  envelope: Envelope;
  onRespond?: EnvelopeResponder;
  /** Increments each time the user sends a new message — triggers dismiss. */
  userMessageCount?: number;
}

export function InterviewCard({ envelope, onRespond, userMessageCount }: InterviewCardProps) {
  // If the backend already recorded a response, show confirmation immediately.
  const alreadyAnswered = envelope.prior_response != null;

  const [dismissed, setDismissed] = useState(false);
  const [submitted, setSubmitted] = useState(alreadyAnswered);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Dismiss when the user sends a new chat message while the card is open.
  const mountCountRef = useRef(userMessageCount);
  useEffect(() => {
    if (userMessageCount !== undefined && mountCountRef.current !== undefined) {
      if (userMessageCount > mountCountRef.current && !submitted) {
        setDismissed(true);
      }
    }
  }, [userMessageCount, submitted]);

  if (dismissed) return null;

  if (submitted) {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-3">
        <p className="text-xs text-success">Answers submitted</p>
      </div>
    );
  }

  return (
    <div className="rounded-md border border-border-subtle bg-bg-elevated">
      {/* Header */}
      {(envelope.title || envelope.subtitle) && (
        <div className="flex items-start justify-between px-3 pt-3 pb-2 border-b border-border-subtle/50">
          <div className="flex-1 min-w-0 pr-2">
            {envelope.title && (
              <p className="text-sm font-semibold text-fg leading-snug">{envelope.title}</p>
            )}
            {envelope.subtitle && (
              <p className="text-xs text-fg-muted mt-0.5 leading-relaxed">{envelope.subtitle}</p>
            )}
          </div>
          <button
            type="button"
            onClick={() => setDismissed(true)}
            className="p-0.5 text-fg-faint hover:text-fg-muted transition-colors shrink-0"
            aria-label="Dismiss"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      )}

      {/* Dismiss button when no header text */}
      {!envelope.title && !envelope.subtitle && (
        <div className="flex justify-end px-2 pt-2">
          <button
            type="button"
            onClick={() => setDismissed(true)}
            className="p-0.5 text-fg-faint hover:text-fg-muted transition-colors"
            aria-label="Dismiss"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      )}

      {/* Body — questions rendered here in later tasks */}
      <div className="px-3 py-3 space-y-4">
        {(envelope.questions ?? []).map((q, i) => (
          <QuestionStub key={i} question={q} />
        ))}
      </div>

      {/* Footer placeholder */}
      <div className="px-3 pb-3 flex justify-end">
        <Button
          size="sm"
          className="bg-primary hover:bg-primary-hover text-white text-xs px-4 py-1 h-7"
          disabled
        >
          Submit
        </Button>
      </div>
    </div>
  );
}

// Temporary stub — replaced in Task 5.
function QuestionStub({ question }: { question: Question }) {
  return (
    <div>
      <label className="block text-xs font-medium text-fg-secondary mb-1">{question.prompt}</label>
      <p className="text-xs text-fg-faint italic">({question.type})</p>
    </div>
  );
}
```

- [ ] **Step 2: TypeScript check**

```bash
cd ~/Projects-apps/nanite/ui && npx tsc -b --noEmit 2>&1 | head -30
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
cd ~/Projects-apps/nanite
git add ui/src/components/chat/envelopes/InterviewCard.tsx
git commit -m "feat(InterviewCard): skeleton with persistence check and confirmation state"
```

---

### Task 5: Build compact input types (text, textarea, radio, checkbox)

**Files:**
- Modify: `ui/src/components/chat/envelopes/InterviewCard.tsx`

This task replaces `QuestionStub` with `QuestionInput` — the full compact rendering for all input types.

- [ ] **Step 1: Add answer state and replace QuestionStub with QuestionInput**

Replace the `QuestionStub` function and update `InterviewCard` to track answers. Replace the entire file content with:

```typescript
import { useState, useRef, useEffect, useCallback } from "react";
import { X, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { type Answer, ResponseStatus } from "@/lib/envelope-response";
import type { Envelope, Question } from "@/lib/types";
import type { EnvelopeResponder } from "./EnvelopeRenderer";

// Normalize an option value: string | {value, label, description?} → {value, label, description?}
function normalizeOption(raw: unknown): { value: string; label: string; description?: string } {
  if (typeof raw === "string") return { value: raw, label: raw };
  if (raw && typeof raw === "object" && "value" in raw) {
    const o = raw as { value: string; label?: string; description?: string };
    return { value: o.value, label: o.label ?? o.value, description: o.description };
  }
  return { value: String(raw), label: String(raw) };
}

interface InterviewCardProps {
  envelope: Envelope;
  onRespond?: EnvelopeResponder;
  userMessageCount?: number;
}

export function InterviewCard({ envelope, onRespond, userMessageCount }: InterviewCardProps) {
  const questions = envelope.questions ?? [];
  const alreadyAnswered = envelope.prior_response != null;

  const [answers, setAnswers] = useState<Record<number, string | string[]>>(() => {
    const initial: Record<number, string | string[]> = {};
    questions.forEach((q, i) => {
      if (q.type === "checkbox") {
        initial[i] = q.default ? [q.default] : [];
      } else {
        initial[i] = q.default ?? "";
      }
    });
    return initial;
  });
  const [errors, setErrors] = useState<Record<number, string>>({});
  const [dismissed, setDismissed] = useState(false);
  const [submitted, setSubmitted] = useState(alreadyAnswered);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const mountCountRef = useRef(userMessageCount);
  useEffect(() => {
    if (userMessageCount !== undefined && mountCountRef.current !== undefined) {
      if (userMessageCount > mountCountRef.current && !submitted) {
        setDismissed(true);
      }
    }
  }, [userMessageCount, submitted]);

  const validate = useCallback((): boolean => {
    const next: Record<number, string> = {};
    questions.forEach((q, i) => {
      if (!q.required) return;
      const val = answers[i];
      const empty = Array.isArray(val) ? val.length === 0 : !val;
      if (empty) next[i] = "Required";
    });
    setErrors(next);
    return Object.keys(next).length === 0;
  }, [questions, answers]);

  const buildAnswers = useCallback(
    (overrideWithDefaults = false): Answer[] =>
      questions.map((q, i) => ({
        questionId: `q-${i}`,
        value: overrideWithDefaults
          ? (answers[i] || q.default || "")
          : (answers[i] ?? ""),
      })),
    [questions, answers],
  );

  const handleSubmit = useCallback(async () => {
    if (!validate()) return;
    setSubmitError(null);
    const typed = buildAnswers();
    setSubmitted(true);
    if (!onRespond) return;
    try {
      await onRespond({ status: ResponseStatus.Submitted, answers: typed });
    } catch (err) {
      setSubmitted(false);
      setSubmitError(err instanceof Error ? err.message : "Failed to submit");
    }
  }, [validate, buildAnswers, onRespond]);

  const handleAcceptSuggested = useCallback(async () => {
    setSubmitError(null);
    const typed = buildAnswers(true);
    setSubmitted(true);
    if (!onRespond) return;
    try {
      await onRespond({ status: ResponseStatus.Submitted, answers: typed });
    } catch (err) {
      setSubmitted(false);
      setSubmitError(err instanceof Error ? err.message : "Failed to submit");
    }
  }, [buildAnswers, onRespond]);

  const hasDefaults = questions.some((q) => q.default != null && q.default !== "");
  const allRequiredHaveDefaults = questions
    .filter((q) => q.required)
    .every((q) => q.default != null && q.default !== "");

  if (dismissed) return null;

  if (submitted) {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-3">
        <p className="text-xs text-success">Answers submitted</p>
      </div>
    );
  }

  return (
    <div className="rounded-md border border-border-subtle bg-bg-elevated">
      {/* Header */}
      {(envelope.title || envelope.subtitle) ? (
        <div className="flex items-start justify-between px-3 pt-3 pb-2 border-b border-border-subtle/50">
          <div className="flex-1 min-w-0 pr-2">
            {envelope.title && (
              <p className="text-sm font-semibold text-fg leading-snug">{envelope.title}</p>
            )}
            {envelope.subtitle && (
              <p className="text-xs text-fg-muted mt-0.5 leading-relaxed">{envelope.subtitle}</p>
            )}
          </div>
          <button
            type="button"
            onClick={() => setDismissed(true)}
            className="p-0.5 text-fg-faint hover:text-fg-muted transition-colors shrink-0"
            aria-label="Dismiss"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      ) : (
        <div className="flex justify-end px-2 pt-2">
          <button
            type="button"
            onClick={() => setDismissed(true)}
            className="p-0.5 text-fg-faint hover:text-fg-muted transition-colors"
            aria-label="Dismiss"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      )}

      {/* Body */}
      <div className="px-3 py-3 space-y-4">
        {questions.map((q, i) => (
          <QuestionInput
            key={i}
            question={q}
            index={i}
            answer={answers[i]}
            error={errors[i]}
            onChange={(val) => {
              setAnswers((a) => ({ ...a, [i]: val }));
              if (errors[i]) setErrors((e) => { const n = { ...e }; delete n[i]; return n; });
            }}
          />
        ))}
      </div>

      {/* Footer */}
      <div className="px-3 pb-3">
        {submitError && (
          <p className="text-xs text-danger mb-2" role="alert">{submitError}</p>
        )}
        <div className="flex items-center justify-between gap-2">
          <div>
            {hasDefaults && allRequiredHaveDefaults && (
              <button
                type="button"
                onClick={() => void handleAcceptSuggested()}
                className="text-xs text-fg-muted hover:text-fg transition-colors"
              >
                Accept suggested
              </button>
            )}
          </div>
          <Button
            size="sm"
            className="bg-primary hover:bg-primary-hover text-white text-xs px-4 py-1 h-7"
            onClick={() => void handleSubmit()}
          >
            Submit
          </Button>
        </div>
      </div>
    </div>
  );
}

interface QuestionInputProps {
  question: Question;
  index: number;
  answer: string | string[];
  error?: string;
  onChange: (val: string | string[]) => void;
}

function QuestionInput({ question, answer, error, onChange }: QuestionInputProps) {
  const isCard = question.displayStyle === "card";

  return (
    <div>
      <label className="block text-xs font-medium text-fg-secondary mb-1">
        {question.prompt}
        {question.required && <span className="text-danger ml-0.5">*</span>}
      </label>

      {question.description && (
        <p className="text-xs text-fg-muted mb-2 leading-relaxed">{question.description}</p>
      )}

      {question.type === "text" && (
        <input
          type="text"
          value={String(answer ?? "")}
          onChange={(e) => onChange(e.target.value)}
          className="w-full bg-bg border border-border rounded px-2 py-1 text-[11px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
        />
      )}

      {question.type === "textarea" && (
        <textarea
          value={String(answer ?? "")}
          onChange={(e) => onChange(e.target.value)}
          rows={3}
          className="w-full bg-bg border border-border rounded px-2 py-1 text-[11px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary resize-none"
        />
      )}

      {question.type === "select" && question.options && (
        <select
          value={String(answer ?? "")}
          onChange={(e) => onChange(e.target.value)}
          className="w-full bg-bg border border-border rounded px-2 py-1 text-[11px] text-fg focus:outline-none focus:ring-1 focus:ring-primary"
        >
          <option value="">Select…</option>
          {question.options.map((raw) => {
            const opt = normalizeOption(raw);
            return <option key={opt.value} value={opt.value}>{opt.label}</option>;
          })}
        </select>
      )}

      {question.type === "radio" && question.options && (
        isCard
          ? <CardOptions
              options={question.options}
              selected={String(answer ?? "")}
              defaultValue={question.default}
              multi={false}
              onChange={(v) => onChange(v as string)}
            />
          : <CompactRadioOptions
              options={question.options}
              selected={String(answer ?? "")}
              defaultValue={question.default}
              onChange={(v) => onChange(v)}
            />
      )}

      {question.type === "checkbox" && question.options && (
        isCard
          ? <CardOptions
              options={question.options}
              selected={Array.isArray(answer) ? answer : []}
              defaultValue={question.default}
              multi={true}
              onChange={(v) => onChange(v as string[])}
            />
          : <CompactCheckboxOptions
              options={question.options}
              selected={Array.isArray(answer) ? answer : []}
              defaultValue={question.default}
              onChange={(v) => onChange(v)}
            />
      )}

      {error && <p className="text-xs text-danger mt-1">{error}</p>}
    </div>
  );
}

interface CompactRadioOptionsProps {
  options: Question["options"];
  selected: string;
  defaultValue?: string;
  onChange: (v: string) => void;
}

function CompactRadioOptions({ options = [], selected, defaultValue, onChange }: CompactRadioOptionsProps) {
  return (
    <div className="space-y-1.5">
      {options.map((raw) => {
        const opt = normalizeOption(raw);
        const isSelected = selected === opt.value;
        const isDefault = defaultValue === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => onChange(opt.value)}
            className="flex items-center gap-2 w-full text-left group"
          >
            <span className={`w-3.5 h-3.5 rounded-full border-2 flex items-center justify-center shrink-0 transition-colors ${
              isSelected ? "border-primary bg-primary" : "border-border-subtle group-hover:border-primary/60"
            }`}>
              {isSelected && <span className="w-1.5 h-1.5 rounded-full bg-white" />}
            </span>
            <span className={`text-xs flex-1 ${isSelected ? "text-fg" : "text-fg-secondary"}`}>
              {opt.label}
            </span>
            {isDefault && !isSelected && (
              <span className="text-[9px] px-1 py-0.5 rounded bg-primary/10 text-primary">Suggested</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

interface CompactCheckboxOptionsProps {
  options: Question["options"];
  selected: string[];
  defaultValue?: string;
  onChange: (v: string[]) => void;
}

function CompactCheckboxOptions({ options = [], selected, defaultValue, onChange }: CompactCheckboxOptionsProps) {
  return (
    <div className="space-y-1.5">
      {options.map((raw) => {
        const opt = normalizeOption(raw);
        const isSelected = selected.includes(opt.value);
        const isDefault = defaultValue === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => {
              onChange(
                isSelected
                  ? selected.filter((v) => v !== opt.value)
                  : [...selected, opt.value],
              );
            }}
            className="flex items-center gap-2 w-full text-left group"
          >
            <span className={`w-3.5 h-3.5 rounded-sm border-2 flex items-center justify-center shrink-0 transition-colors ${
              isSelected ? "border-primary bg-primary" : "border-border-subtle group-hover:border-primary/60"
            }`}>
              {isSelected && <Check className="w-2.5 h-2.5 text-white" />}
            </span>
            <span className={`text-xs flex-1 ${isSelected ? "text-fg" : "text-fg-secondary"}`}>
              {opt.label}
            </span>
            {isDefault && !isSelected && (
              <span className="text-[9px] px-1 py-0.5 rounded bg-primary/10 text-primary">Suggested</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

interface CardOptionsProps {
  options: Question["options"];
  selected: string | string[];
  defaultValue?: string;
  multi: boolean;
  onChange: (v: string | string[]) => void;
}

function CardOptions({ options = [], selected, defaultValue, multi, onChange }: CardOptionsProps) {
  const selectedArr = Array.isArray(selected) ? selected : [selected];

  return (
    <div className="space-y-2">
      {options.map((raw) => {
        const opt = normalizeOption(raw);
        const isSelected = selectedArr.includes(opt.value);
        const isDefault = defaultValue === opt.value;

        const handleClick = () => {
          if (multi) {
            const arr = selectedArr.includes(opt.value)
              ? selectedArr.filter((v) => v !== opt.value)
              : [...selectedArr, opt.value];
            onChange(arr);
          } else {
            onChange(opt.value);
          }
        };

        return (
          <button
            key={opt.value}
            type="button"
            onClick={handleClick}
            className={`w-full text-left rounded-md border-2 px-3 py-2.5 transition-colors ${
              isSelected
                ? "border-primary bg-primary/5"
                : "border-border-subtle hover:border-primary/40"
            }`}
          >
            <div className="flex items-start justify-between gap-2">
              <span className={`text-xs font-medium ${isSelected ? "text-fg" : "text-fg-secondary"}`}>
                {opt.label}
              </span>
              <div className="flex items-center gap-1.5 shrink-0">
                {isDefault && !isSelected && (
                  <span className="text-[9px] px-1 py-0.5 rounded bg-primary/10 text-primary">
                    Suggested
                  </span>
                )}
                {multi ? (
                  <span className={`w-3.5 h-3.5 rounded-sm border-2 flex items-center justify-center transition-colors ${
                    isSelected ? "border-primary bg-primary" : "border-border-subtle"
                  }`}>
                    {isSelected && <Check className="w-2.5 h-2.5 text-white" />}
                  </span>
                ) : (
                  <span className={`w-3.5 h-3.5 rounded-full border-2 flex items-center justify-center transition-colors ${
                    isSelected ? "border-primary bg-primary" : "border-border-subtle"
                  }`}>
                    {isSelected && <span className="w-1.5 h-1.5 rounded-full bg-white" />}
                  </span>
                )}
              </div>
            </div>
            {opt.description && (
              <p className="text-[11px] text-fg-muted mt-1 leading-relaxed">{opt.description}</p>
            )}
          </button>
        );
      })}
    </div>
  );
}
```

- [ ] **Step 2: TypeScript check**

```bash
cd ~/Projects-apps/nanite/ui && npx tsc -b --noEmit 2>&1 | head -30
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
cd ~/Projects-apps/nanite
git add ui/src/components/chat/envelopes/InterviewCard.tsx
git commit -m "feat(InterviewCard): full compact + card input types with accept-suggested and dismiss"
```

---

### Task 6: Wire InterviewCard into EnvelopeRenderer and update registry

**Files:**
- Modify: `ui/src/components/chat/envelopes/EnvelopeRenderer.tsx`
- Modify: `ui/src/components/chat/ChatTranscript.tsx`
- Modify: `config/envelopes.yaml`
- Delete: `ui/src/components/chat/envelopes/QuestionForm.tsx`

- [ ] **Step 1: Add `userMessageCount` to `ChatTranscript`**

In `ui/src/components/chat/ChatTranscript.tsx`:

Find the `ChatTranscriptProps` interface and add:
```typescript
interface ChatTranscriptProps {
  messages: Message[];
  // ... existing props ...
}
```

Add before the `return` in the `ChatTranscript` function:
```typescript
const userMessageCount = messages.filter((m) => m.role === "user").length;
```

Find where `EnvelopeRenderer` is rendered in the message loop (around line 383) and add the prop:
```tsx
<EnvelopeRenderer
  envelope={parsedEnvelope}
  {...(onSendMessage && { onSendMessage })}
  userMessageCount={userMessageCount}
/>
```

Also find the plugin envelope render (around line 423) and add:
```tsx
<EnvelopeRenderer
  envelope={item.envelope}
  {...(onSendMessage && { onSendMessage })}
  userMessageCount={userMessageCount}
/>
```

Note: the first argument to `EnvelopeRenderer` in `ChatTranscript` might be named differently than `parsedEnvelope` — read the file around line 383 to find the exact variable name before making edits.

- [ ] **Step 2: Add `userMessageCount` to `EnvelopeRendererProps` and pass to `InterviewCard`**

In `ui/src/components/chat/envelopes/EnvelopeRenderer.tsx`:

Add `userMessageCount?: number` to `EnvelopeRendererProps`:
```typescript
interface EnvelopeRendererProps {
  envelope: Envelope;
  onSendMessage?: (content: string) => void;
  onEnvelopeResponse?: (response: ResponseV1) => Promise<void>;
  userMessageCount?: number;   // NEW
}
```

Update the function signature:
```typescript
export function EnvelopeRenderer({
  envelope,
  onSendMessage,
  onEnvelopeResponse,
  userMessageCount,
}: EnvelopeRendererProps) {
```

In the fallback section, replace the `QuestionForm` render with `InterviewCard`:

```typescript
// Remove this import at top:
import { QuestionForm } from "./QuestionForm";

// Add this import instead:
import { InterviewCard } from "./InterviewCard";
```

Replace the fallback questions block:
```tsx
{envelope.questions && envelope.questions.length > 0 && (
  <InterviewCard
    envelope={envelope}
    {...(envelope.id ? { onRespond } : {})}
    userMessageCount={userMessageCount}
  />
)}
```

- [ ] **Step 3: TypeScript check**

```bash
cd ~/Projects-apps/nanite/ui && npx tsc -b --noEmit 2>&1 | head -30
```

Expected: no errors.

- [ ] **Step 4: Update `config/envelopes.yaml`**

In `config/envelopes.yaml`, find the `question-form` entry and remove `component` and `export` so it becomes backend-only:

```yaml
  - type: question-form
    # backend-only — InterviewCard renders via EnvelopeRenderer fallback path
```

- [ ] **Step 5: Regenerate plugin registry**

```bash
cd ~/Projects-apps/nanite/ui && npm run generate:plugins
```

Expected: `ui/src/generated/plugin-envelopes.ts` is updated, `question-form` entry removed.

- [ ] **Step 6: Verify the registry sync test passes**

```bash
cd ~/Projects-apps/nanite && go test ./internal/chat/... -run TestEnvelopeRegistrySync -v
```

Expected: PASS.

- [ ] **Step 7: Delete QuestionForm**

```bash
rm ~/Projects-apps/nanite/ui/src/components/chat/envelopes/QuestionForm.tsx
```

- [ ] **Step 8: Final TypeScript check**

```bash
cd ~/Projects-apps/nanite/ui && npx tsc -b --noEmit 2>&1 | head -30
```

Expected: no errors. If `QuestionForm` was imported somewhere else, that error surfaces here — fix any remaining import references.

- [ ] **Step 9: Run full backend tests**

```bash
cd ~/Projects-apps/nanite && go test ./... 2>&1 | grep -E "FAIL|ok|---"
```

Expected: all pass.

- [ ] **Step 10: Commit**

```bash
cd ~/Projects-apps/nanite
git add ui/src/components/chat/envelopes/EnvelopeRenderer.tsx \
        ui/src/components/chat/ChatTranscript.tsx \
        config/envelopes.yaml \
        ui/src/generated/plugin-envelopes.ts
git rm ui/src/components/chat/envelopes/QuestionForm.tsx
git commit -m "feat(InterviewCard): wire into EnvelopeRenderer, remove QuestionForm, update registry"
```

---

### Task 7: Verify persistence — submit, hard refresh, confirmation row

This task is a mandatory manual verification step. No code changes — it confirms the persistence fix works end-to-end.

- [ ] **Step 1: Build and run the app**

```bash
cd ~/Projects-apps/nanite && make dev
```

Or the equivalent dev command for the project. The app should run at `http://localhost:PORT`.

- [ ] **Step 2: Trigger a question-form envelope**

In an active chat session, trigger an agent action that emits a `question-form` envelope. Alternatively, use a dev fixture or the `recover_mode` UI to inject one manually.

- [ ] **Step 3: Submit the form**

Fill in at least one answer and click Submit. Confirm the card collapses to the green "Answers submitted" confirmation row.

- [ ] **Step 4: Hard refresh**

Press `Cmd+Shift+R` (or `Ctrl+Shift+R`). The page reloads, messages are re-fetched from the API.

- [ ] **Step 5: Verify the confirmation row**

The card that was submitted should still show "Answers submitted" — NOT the live form. If the live form appears instead, the `prior_response` injection is not working. Debug by:
1. Checking the network response for `GET /api/sessions/:id/messages` — look for `prior_response` in the envelope JSON
2. Verifying `buildEnvelopeLookup` is called correctly in `handleListSessionMessages`

- [ ] **Step 6: Commit (if any debug fixes were made)**

```bash
cd ~/Projects-apps/nanite
git add -p
git commit -m "fix(api): correct prior_response injection for refresh persistence"
```

---

## Self-Review Checklist

After completing all tasks, verify:

- [ ] `go test ./...` passes clean
- [ ] `tsc -b --noEmit` in `ui/` passes clean
- [ ] `QuestionForm.tsx` is deleted and not referenced anywhere
- [ ] `question-form` in `envelopes.yaml` has no `component` or `export`
- [ ] Hard-refresh test from Task 7 passes
- [ ] Card-style options render for questions with `displayStyle: "card"`
- [ ] Compact options render for questions with `displayStyle: "compact"` or unset
- [ ] "Suggested" badge appears on the agent's default option
- [ ] "Accept suggested" is hidden when any required question lacks a default
- [ ] Dismiss removes the card without calling `onRespond`
