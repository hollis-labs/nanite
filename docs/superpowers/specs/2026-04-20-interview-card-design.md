# InterviewCard Design Spec

**Date:** 2026-04-20  
**Status:** Approved  
**Replaces:** `QuestionForm` (`ui/src/components/chat/envelopes/QuestionForm.tsx`)

## Overview

`InterviewCard` is a redesigned renderer for the `question` envelope kind. It replaces the demo-quality `QuestionForm` with a polished, first-class card component that follows the new visual language established by `TodoItem`. The backend envelope kind (`question`) and typed response protocol are unchanged — this is a pure frontend redesign with additive schema extensions.

## Approach

Approach C: `InterviewCard` becomes the sole renderer for the `question` envelope. `QuestionForm` is deleted. Schema extensions to `Question` and the envelope payload are fully optional and backwards-compatible — old payloads without the new fields render identically to today.

## Data Model

### `Question` type extensions

```typescript
export interface Question {
  prompt: string;
  type: "text" | "textarea" | "select" | "radio" | "checkbox";
  options?: (string | { value: string; label: string; description?: string })[];
  required: boolean;
  default?: string;
  description?: string;      // paragraph shown under prompt in card display
  displayStyle?: "compact" | "card";  // defaults to "compact"
}
```

- `displayStyle: "compact"` — tight row layout, label + input inline
- `displayStyle: "card"` — label + description paragraph + input below; for radio/checkbox, each option is its own bordered card
- `options` entries can now be objects carrying a `description` for card-style option detail paragraphs
- All new fields are optional; existing payloads default to compact with no descriptions

### `Envelope` type extensions

`title`, `subtitle`, and `prior_response` are added as first-class fields alongside `questions`:

```typescript
export interface Envelope {
  // ... existing fields ...
  title?: string;                // e.g. "Phase 3 Design Options — Pick 1"
  subtitle?: string;             // optional subheading or special instructions
  prior_response?: ResponseV1;   // set by backend when envelope already answered
}
```

- `title`/`subtitle` are optional — old payloads without them render without a header section
- `prior_response` is set by the backend when serving conversation history for an envelope that has already been responded to. `InterviewCard` checks this on mount: if present, render confirmation row immediately without showing the form.

**Note:** `prior_response` requires a small backend change — the envelope serializer must include the prior `ResponseV1` payload when the envelope has been responded to. This is the fix for the refresh/re-render bug.

## Component Structure

### Header

- `title` in `text-sm font-semibold text-fg`
- Optional `subtitle` in `text-xs text-fg-muted` below title
- Dismiss `×` button pinned top-right

### Body

Questions rendered in order, separated by subtle dividers.

**Compact questions (`displayStyle: "compact"` or unset):**
- Label (`prompt`) in `text-xs font-medium text-fg-secondary`
- Input immediately below in the same tight style as the rest of the card
- Native browser chrome is not used — all inputs are custom-styled to match the design system

**Card questions (`displayStyle: "card"`):**
- Label (`prompt`) in `text-xs font-medium text-fg-secondary`
- `description` paragraph in `text-xs text-fg-muted` below the label
- Input below the description

**Card-style radio/checkbox options:**
- Each option is a bordered card: `rounded-md border-2 border-border-subtle`
- Contains option `label` (bold) + optional option `description` paragraph
- Selection indicator (radio dot / checkmark) at top-right of card
- Clicking anywhere on the card selects/toggles it
- Selected state: `border-primary bg-primary/5`
- Agent default option gets a subtle `Suggested` badge (`text-[9px] text-primary bg-primary/10 rounded px-1`)

**Compact-style radio/checkbox options:**
- Tight rows with custom-styled dot/checkmark (not native `<input type="radio/checkbox">`)
- `accent-accent` is replaced with fully custom styling matching the design system
- Agent default is pre-selected

**Text / textarea inputs:**
- `default` value pre-fills the input
- Styled consistent with `TodoItem` inline feedback input: `bg-bg border border-border rounded px-2 py-1 text-[11px]`

### Footer

- **Left:** `Accept suggested` button (ghost/subtle) — visible only when at least one question has a `default`. If a `required` question has no default, the button is hidden entirely.
- **Right:** `Submit` button (primary)

### Post-submit state

Card collapses to a compact confirmation row:
```
border-success/30 bg-success/5 rounded-sm p-3
text-xs text-success  "Answers submitted"
```

## Interactions

### Submit
1. Validate all `required` questions — empty required inputs show a per-question inline error (`text-xs text-danger` below the input), Submit is blocked
2. Fire `onRespond({ status: ResponseStatus.Submitted, answers: Answer[] })`
3. On success: collapse to confirmation row, resume chat
4. On error: show error inline, keep form open

### Accept suggested
1. Merge agent defaults with any user edits (user edits win over defaults)
2. Fire `onRespond` immediately — same path as Submit
3. Bypasses required-field validation when all required questions have a default
4. Card collapses to confirmation row

### Dismiss
1. Remove card from view
2. No `onRespond` call — agent receives no answer
3. Resumes chat

### Chat reply while card is open
1. `InterviewCard` subscribes to a "message sent" signal from the chat store
2. On fire, calls dismiss handler (same as Dismiss above)
3. The chat message is sent normally

## Persistence (critical)

**The `submitted` state must be derived from `envelope.prior_response`, not from React `useState`.**

The current `QuestionForm` uses `useState(false)` for `submitted`, which resets on refresh. This causes the card to re-render as a live form after a page reload, and the agent re-asks the question.

`InterviewCard` initializes by checking `envelope.prior_response` (passed through from `EnvelopeRenderer`). If set, it renders the confirmation row immediately — the live form is never shown. `useState` is only used as a local optimistic flag for the submit-in-progress case; the source of truth is always the prop.

This requires `EnvelopeRenderer` to pass the full `envelope` object (or at minimum `envelope.prior_response`) to `InterviewCard`, and requires the backend envelope serializer to populate `prior_response` when the envelope has been answered.

**Required verification step in implementation:** submit answers → hard-refresh the page → confirm the confirmation row renders, not the live form.

## Visual Reference

The component follows the visual language of `TodoItem` (`ui/src/components/work/TodoItem.tsx`):
- Container: `rounded-md border border-border-subtle bg-bg-elevated`
- Tight spacing, small text (`text-xs`, `text-[11px]` for secondary elements)
- Custom-styled interactive elements (no native browser chrome)
- Inline expand pattern for contextual sub-content (see TodoItem's `reopenFeedback` pattern)

## Files Affected

| File | Action |
|------|--------|
| `ui/src/components/chat/envelopes/InterviewCard.tsx` | Create |
| `ui/src/components/chat/envelopes/QuestionForm.tsx` | Delete |
| `ui/src/lib/types.ts` | Extend `Question` + `Envelope` interfaces |
| `ui/src/components/chat/envelopes/EnvelopeRenderer.tsx` | Swap `QuestionForm` → `InterviewCard`, pass `envelope` prop |
| `ui/src/components/chat/envelopes/index.ts` (if exists) | Update export |
| Backend envelope serializer | Populate `prior_response` when envelope already answered |

## Out of Scope

- Sidebar/non-blocking persistent version of this card (planned for later)
- Redesign of other envelope cards (planned as a separate pass)
- `select` dropdown input type — present in the type union but lowest priority; can fall back to compact styled `<select>` for now
- Backend schema changes beyond `prior_response` in envelope serializer
