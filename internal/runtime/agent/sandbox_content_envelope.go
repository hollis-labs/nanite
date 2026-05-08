package agent

// envelopeSchemaContent is the .sandbox/envelope-schema.md body planted in
// every nanite-managed boot dir, regardless of provider. The text mirrors
// internal/plugin/builtin/adapter-claude/plugin.go's constant verbatim;
// when the Phase 4c migration deletes the duplicate, this file becomes the
// single source of truth.
const envelopeSchemaContent = `# Nanite Envelope Schema

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

Use kind="envelope" with a registered type for display cards (kb-result, giphy-modal, report-card, etc.). Use kind="question"/"action"/"approval" for interactive forms and proposals.

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

## Registered Envelope Types

These are the only types the frontend can render. Using any other type
causes the envelope to be silently dropped — no error, no warning.

| Type | Purpose |
|------|---------|
| giphy-modal | Giphy search/selection |
| document-viewer | Document display card |
| report-card | Summary/report display |
| kb-result | Knowledge base search result |
| ticket-confirmation | Support ticket confirmation |
| ticket-form | Support ticket input form |
| resolution-capture | Issue resolution capture |

## Examples

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
