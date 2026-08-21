package agent

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/chat"
)

// envelopeSchemaContent returns the .sandbox/envelope-schema.md body
// planted in every nanite-managed boot dir, regardless of provider.
//
// The "Registered Envelope Types" section is built at call time from
// chat.RegisteredEnvelopeTypeNames() / chat.EnvelopeRegistry() — the live,
// in-process registry populated at startup from the external
// github.com/hollis-labs/go-envelopes module's embedded manifest, plus any
// plugin-registered types (internal/plugin's Host.RegisterEnvelope). Do NOT
// hardcode a static type table here again: a hand-maintained list silently
// drifts every time a core type is added/removed or a plugin (un)registers
// one, which is exactly the staleness this function replaced (Phase 6 task
// 06 — the table used to list 7 fixed types, 5 of which were already cut).
//
// This used to be a package-level const string literal that duplicated
// (per its own doc comment) a copy once carried in
// internal/plugin/builtin/adapter-claude/plugin.go. That duplicate is long
// gone — adapter-claude's PopulateSandbox has been a no-op since Phase
// 4c.6 (CW-20260508-0002); this function is the sole source today.
func envelopeSchemaContent() string {
	var b strings.Builder
	b.WriteString(envelopeSchemaHeader)
	b.WriteString(registeredEnvelopeTypesSection())
	b.WriteString(envelopeSchemaExamples)
	return b.String()
}

// registeredEnvelopeTypesSection renders the "## Registered Envelope
// Types" table. The type-name column comes from
// chat.RegisteredEnvelopeTypeNames() (the complete set ValidateEnvelope
// actually accepts); the Source column is enriched from
// chat.EnvelopeRegistry() where available (core types are always present
// there; a plugin type registered without a JSON Schema is not, and falls
// back to "plugin" since only plugins register types outside InitCoreTypes).
func registeredEnvelopeTypesSection() string {
	var b strings.Builder
	b.WriteString("## Registered Envelope Types\n\n")
	b.WriteString("These are the only types the frontend can render. Using any other type\n")
	b.WriteString("causes the envelope to be silently dropped — no error, no warning.\n\n")

	names := chat.RegisteredEnvelopeTypeNames()
	if len(names) == 0 {
		b.WriteString("_(envelope registry was not yet populated when this boot dir was planted — " +
			"this should not happen outside of tests; report it if seen against a live agent boot.)_\n\n")
		return b.String()
	}

	reg := chat.EnvelopeRegistry()
	b.WriteString("| Type | Source |\n")
	b.WriteString("|------|--------|\n")
	for _, name := range names {
		source := "plugin"
		if reg != nil {
			if spec, ok := reg.Lookup(name); ok {
				source = spec.Source.String()
			}
		}
		fmt.Fprintf(&b, "| %s | %s |\n", name, source)
	}
	b.WriteString("\n")
	return b.String()
}

// envelopeSchemaHeader is the static preamble: wire format + field
// reference. Unlike the type table above, this describes the envelope
// wire shape itself (kind/version/type/data and the question/action/
// approval interactive pattern), which doesn't change per-registration —
// no dynamic sourcing needed here.
const envelopeSchemaHeader = `# Nanite Envelope Schema

## Format

Wrap envelopes in a fenced code block with the ` + "`nanite-envelope`" + ` language tag.

There are two envelope patterns:

### Interactive envelopes (user input)
` + "```" + `nanite-envelope
{
  "kind": "question|action|approval",
  "version": 1,
  "type": "nanite",
  "questions": [...],
  "proposals": [...],
  "approval": {...},
  "notes": "Brief explanation"
}
` + "```" + `

### Plugin/display envelopes (rich UI cards)
` + "```" + `nanite-envelope
{
  "kind": "envelope",
  "version": 1,
  "type": "<registered-type>",
  "data": { ... }
}
` + "```" + `

Use kind="envelope" with a registered type for display cards (report-card, metric-card, etc.). Use kind="question"/"action"/"approval" for interactive forms and proposals.

## Field Reference

### Root Fields
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| kind | string | yes | "question", "action", "approval", or "envelope" |
| version | number | yes | Always 1 |
| type | string | yes | "nanite" for interactive envelopes; a registered type name for display envelopes |
| data | object | conditional | Required when kind="envelope" — card-specific payload |
| questions | array | conditional | Required when kind="question" |
| proposals | array | conditional | Required when kind="action" |
| approval | object | conditional | Required when kind="approval" |
| notes | string | no | Brief explanation shown to user |

### Question Object
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| prompt | string | yes | The question text |
| type | string | yes | "text", "textarea", "select", "radio", "checkbox" |
| options | array | conditional | Required for select, radio, checkbox |
| required | boolean | no | Whether the field must be filled |
| default | string | no | Default value |

### Proposal Object
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Action type (e.g. "create_task", "update_sprint") |
| payload | object | yes | The data to create/modify |
| schema | object | no | Editable field definitions for user review |

### Approval Object
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| action | string | yes | What will happen if approved |
| risk | string | yes | "low", "medium", or "high" |
| details | object | no | Additional context for the user |

`

// envelopeSchemaExamples is the static closing section: worked examples of
// each interactive-envelope kind. Not type-list content, so it doesn't
// need dynamic sourcing.
const envelopeSchemaExamples = `## Examples

### Question envelope
` + "```" + `nanite-envelope
{
  "kind": "question",
  "version": 1,
  "type": "nanite",
  "questions": [
    {
      "prompt": "What priority should this task have?",
      "type": "select",
      "options": ["low", "medium", "high", "critical"],
      "required": true,
      "default": "medium"
    }
  ],
  "notes": "Setting priority for the new task"
}
` + "```" + `

### Action envelope (proposal)
` + "```" + `nanite-envelope
{
  "kind": "action",
  "version": 1,
  "type": "nanite",
  "proposals": [
    {
      "type": "create_task",
      "payload": {
        "title": "Fix login redirect bug",
        "project_id": "proj-123",
        "priority": "high"
      },
      "schema": {
        "title": {"type": "text", "editable": true},
        "priority": {"type": "select", "options": ["low", "medium", "high"], "editable": true}
      }
    }
  ],
  "notes": "Review and approve the new task"
}
` + "```" + `

### Approval envelope
` + "```" + `nanite-envelope
{
  "kind": "approval",
  "version": 1,
  "type": "nanite",
  "approval": {
    "action": "Archive 12 completed tasks from Sprint 4",
    "risk": "medium",
    "details": {
      "task_count": 12,
      "sprint": "Sprint 4"
    }
  },
  "notes": "This will archive all completed tasks and remove them from active views"
}
` + "```" + `
`
