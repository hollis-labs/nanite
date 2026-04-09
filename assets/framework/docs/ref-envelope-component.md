# build-envelope-component

Build rich interactive UI cards (envelopes) that render inside Conduit chat messages. Envelopes are the bridge between agent responses and interactive React components. The canonical examples are in the `support-ticket` plugin: KBResultCard, TicketFormCard, TicketConfirmationCard, ResolutionCaptureCard.

## When to Use

- When building a new envelope component for a plugin
- When adding interactive UI to agent chat responses
- When an agent needs to display structured data (forms, cards, results, media)

## Envelope JSON Structure

Agents emit envelopes as fenced code blocks in their responses. The Conduit frontend detects these and renders the matching React component.

```
```conduit-envelope
{"kind":"envelope","version":1,"type":"my-type","data":{...}}
```
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `kind` | string | Yes | Always `"envelope"` |
| `version` | number | Yes | Always `1` |
| `type` | string | Yes | Maps to a registered component (e.g. `"ticket-form"`, `"kb-result"`) |
| `data` | object | Yes | Passed as `data` prop to the React component |

### How the Agent Emits an Envelope

The agent's system prompt must instruct it to emit the fenced code block. Example from the IT Support agent:

```
When the user needs a ticket, emit this envelope:
```conduit-envelope
{"kind":"envelope","version":1,"type":"ticket-form","data":{"categories":["network","access","vpn","jira"],"prefilled":{"title":"...","description":"...","category":"..."}}}
```

Pre-fill the form fields from the conversation context.
```

Key rules for agent instructions:
- The agent must emit the JSON inside a `conduit-envelope` fenced code block
- The `type` field MUST match a registered envelope type exactly
- The `data` object schema must match what the component expects
- Tell the agent what fields to pre-fill from conversation context

## React Component Pattern

Every envelope component follows this pattern:

```typescript
// 1. Define the data interface (matches the "data" field in the JSON)
interface MyCardData {
  title: string
  items: Array<{ id: string; label: string }>
  showActions?: boolean
}

// 2. Define the props interface — always has data + optional onSendMessage
interface MyCardProps {
  data: MyCardData
  onSendMessage?: (content: string) => void
}

// 3. Export a named function component
export function MyCard({ data, onSendMessage }: MyCardProps) {
  return (
    <div className="rounded-lg border border-zinc-700 bg-zinc-900/50 p-4">
      {/* Component content */}
    </div>
  )
}
```

### Props Contract

| Prop | Type | Always Present | Description |
|------|------|----------------|-------------|
| `data` | `YourDataType` | Yes | The `data` field from the envelope JSON |
| `onSendMessage` | `(content: string) => void` | No | Callback to send a message back to the chat as the user |

`onSendMessage` is provided by `EnvelopeRenderer.tsx` — it sends a message into the chat stream as if the user typed it. This is how interactive envelopes drive the conversation.

## How Rendering Works

The dispatch chain:

1. Agent emits `conduit-envelope` fenced code block in its response
2. Conduit frontend parser extracts the JSON envelope
3. `EnvelopeRenderer.tsx` looks up `envelope.type` in `PLUGIN_ENVELOPE_REGISTRY`
4. If found, renders the matching component inside a `<Suspense>` boundary
5. Passes `envelope.data` as `data` prop and `onSendMessage` callback

From `EnvelopeRenderer.tsx`:

```typescript
const PluginComponent = PLUGIN_ENVELOPE_REGISTRY[envelope.type]
if (PluginComponent && envelope.data) {
  return (
    <Suspense fallback={<div className="animate-pulse p-4 text-sm text-zinc-400">Loading...</div>}>
      <PluginComponent data={envelope.data} {...(onSendMessage ? { onSendMessage } : {})} />
    </Suspense>
  )
}
```

## Registration (Three Steps)

### Step 1: plugin.yaml

Add the envelope to your plugin manifest:

```yaml
registers:
  envelopes:
    - type: my-card              # Must match the "type" in envelope JSON
      component: ui/MyCard       # Path relative to ui/src/ (no .tsx extension)
      export: MyCard             # Named export from the TSX file
```

### Step 2: TSX File

Place the component at the path declared in `component`. For a plugin named `my-plugin`:

```
plugins/my-plugin/ui/MyCard.tsx
```

The codegen script resolves this path relative to `ui/src/`, so the actual file must be at:

```
ui/src/ui/MyCard.tsx
```

Or if your plugin registers components in the envelopes directory:

```
ui/src/components/chat/envelopes/MyCard.tsx
```

Adjust the `component` path in plugin.yaml accordingly.

### Step 3: Run Codegen

```bash
cd ui && npm run generate:plugins
```

This runs `scripts/generate-plugin-imports.mjs` which:
1. Scans all `plugins/*/plugin.yaml` files for envelope registrations
2. Verifies each TSX file exists at the declared path
3. Generates lazy imports in `ui/src/generated/plugin-envelopes.ts`

The generated entry looks like:

```typescript
// @PLUGIN_ENTRIES_START
const PLUGIN_ENVELOPE_ENTRIES: Record<string, LazyEnvelopeComponent> = {
  'my-card': lazy(() => import('@/ui/MyCard').then(m => ({ default: m.MyCard }))),
};
// @PLUGIN_ENTRIES_END
```

Core envelopes in `CORE_ENVELOPE_REGISTRY` are never touched by codegen.

## Interactive Patterns

### Button Click -> Chat Message

The most common pattern. Button clicks send a command back to the chat:

```typescript
export function MyCard({ data, onSendMessage }: MyCardProps) {
  return (
    <div className="rounded-lg border border-zinc-700 bg-zinc-900/50 p-4">
      <Button
        size="sm"
        className="bg-indigo-600 hover:bg-indigo-500 text-white text-xs px-3 py-1 h-7"
        onClick={() => {
          if (onSendMessage) {
            onSendMessage('!mycommand process-item abc-123')
          }
        }}
      >
        Process Item
      </Button>
    </div>
  )
}
```

The agent (or an event hook) can then detect `!mycommand` in the user's message and act on it.

### Form Submission -> Chat Message

Forms collect user input and send it as a structured message. From TicketFormCard:

```typescript
const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
  e.preventDefault()
  if (!title.trim() || !category || !description.trim()) return

  setFormState('submitting')

  try {
    // Call the plugin's CRUD API directly
    const res = await fetch('/api/plugins/tickets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: title.trim(),
        category,
        priority,
        description: description.trim(),
      }),
    })

    if (!res.ok) throw new Error(await res.text())
    const result = await res.json()
    setFormState('success')

    // Notify the chat about what happened
    if (onSendMessage) {
      onSendMessage(
        `Ticket created: ${result.id} — ${title.trim()} [Category: ${category}]`
      )
    }
  } catch (err) {
    setFormState('error')
    setErrorMsg(err instanceof Error ? err.message : 'Failed')
  }
}
```

Key pattern: the form calls the plugin's REST API directly (`/api/plugins/tickets`), then uses `onSendMessage` to inform the chat of the result.

### Feedback Buttons (Solved / Not Solved)

From KBResultCard — let the user indicate whether the content helped:

```typescript
const [feedback, setFeedback] = useState<'none' | 'solved' | 'ticket'>('none')

{feedback === 'none' && (
  <div className="rounded-lg border border-zinc-700 bg-zinc-900/50 p-4">
    <p className="text-sm text-zinc-300 mb-3">Did this resolve your issue?</p>
    <div className="flex items-center gap-2">
      <Button
        size="sm"
        className="bg-green-600 hover:bg-green-500 text-white text-xs px-3 py-1 h-7"
        onClick={() => {
          setFeedback('solved')
          if (onSendMessage) {
            onSendMessage('The article resolved my issue. Thanks!')
          }
        }}
      >
        <CheckCircle className="mr-1.5 h-3 w-3" />
        Yes, solved
      </Button>
      <Button
        size="sm"
        className="bg-zinc-700 hover:bg-zinc-600 text-zinc-200 text-xs px-3 py-1 h-7"
        onClick={() => setFeedback('ticket')}
      >
        <Ticket className="mr-1.5 h-3 w-3" />
        No, open a ticket
      </Button>
    </div>
  </div>
)}
```

### Multi-State Cards

Envelopes often have state transitions: idle -> submitting -> success/error. From TicketFormCard:

```typescript
type FormState = 'idle' | 'submitting' | 'success' | 'error'

const [formState, setFormState] = useState<FormState>('idle')

// Render different views based on state
if (formState === 'success') {
  return (
    <div className="rounded-lg border border-green-500/30 bg-zinc-900/50 overflow-hidden">
      <div className="flex items-center gap-2 bg-green-500/5 px-4 py-3">
        <CheckCircle className="h-4 w-4 text-green-400" />
        <span className="text-sm text-green-400">Success!</span>
      </div>
    </div>
  )
}

// Default: render the form
return (
  <div className="rounded-lg border border-zinc-700 bg-zinc-900/50 p-4">
    <form onSubmit={handleSubmit}>...</form>
  </div>
)
```

### Loading / Skeleton States

The `EnvelopeRenderer` wraps every component in `<Suspense>` with a built-in loading state:

```typescript
<Suspense fallback={<div className="animate-pulse p-4 text-sm text-zinc-400">Loading...</div>}>
```

For internal loading states (e.g., during API calls):

```typescript
{formState === 'submitting' && (
  <Button disabled>
    <Loader2 className="mr-1.5 h-3 w-3 animate-spin" />
    Creating...
  </Button>
)}
```

### Error States

Always handle errors gracefully:

```typescript
{formState === 'error' && (
  <div className="flex items-center gap-2 rounded-md border border-red-500/30 bg-red-500/5 px-3 py-2">
    <AlertCircle className="h-4 w-4 shrink-0 text-red-400" />
    <span className="text-xs text-red-400">{errorMsg}</span>
  </div>
)}
```

## Styling Guide

Conduit uses Tailwind CSS with a dark theme. Follow these patterns from existing envelopes:

### Card Container

```typescript
// Standard card
<div className="rounded-lg border border-zinc-700 bg-zinc-900/50 p-4">

// Success state card
<div className="rounded-lg border border-green-500/30 bg-zinc-900/50 overflow-hidden">

// Error state card
<div className="rounded-lg border border-red-500/30 bg-red-500/5 px-3 py-2">

// Info/note card
<div className="flex items-start gap-2 rounded-md border border-zinc-700 bg-zinc-800/50 px-3 py-2">
```

### Headers

```typescript
// Card header with icon
<div className="mb-4 flex items-center gap-2">
  <Ticket className="h-4 w-4 text-zinc-400" />
  <h4 className="text-sm font-medium text-zinc-200">Card Title</h4>
</div>

// Success header bar
<div className="flex items-center justify-between bg-green-500/5 px-4 py-3 border-b border-green-500/20">
  <div className="flex items-center gap-2">
    <CheckCircle className="h-4 w-4 text-green-400" />
    <span className="text-sm font-medium text-zinc-200">Title</span>
  </div>
  <span className="inline-block rounded-full bg-green-500/15 border border-green-500/25 px-2 py-0.5 text-xs text-green-400">
    status
  </span>
</div>
```

### Labels and Values

```typescript
// Label above value
<div>
  <span className="block text-xs font-medium text-zinc-400 mb-0.5">Label</span>
  <span className="text-sm text-zinc-200">Value</span>
</div>

// Required field label
<label className="mb-1 block text-xs font-medium text-zinc-400">
  Field Name <span className="text-red-400">*</span>
</label>
```

### Inputs

```typescript
// Standard input class (reusable)
const inputCls =
  'w-full bg-zinc-800 border border-zinc-700 rounded-md px-2.5 py-1.5 text-sm text-zinc-200 outline-none focus:border-indigo-500 placeholder:text-zinc-600'

// Text input
<input type="text" className={inputCls} />

// Textarea
<textarea className={`${inputCls} min-h-[80px] resize-y`} rows={3} />

// Select
<select className={inputCls}>
  <option value="">Select...</option>
</select>
```

### Buttons

```typescript
// Primary action
<Button
  size="sm"
  className="bg-indigo-600 hover:bg-indigo-500 text-white text-xs px-4 py-1 h-8"
>
  Submit
</Button>

// Success action
<Button
  size="sm"
  className="bg-green-600 hover:bg-green-500 text-white text-xs px-3 py-1 h-7"
>
  Confirm
</Button>

// Secondary/neutral action
<Button
  size="sm"
  className="bg-zinc-700 hover:bg-zinc-600 text-zinc-200 text-xs px-3 py-1 h-7"
>
  Cancel
</Button>

// Subtle action
<Button
  size="sm"
  className="bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs px-3 py-1 h-7"
>
  <Download className="mr-1.5 h-3 w-3" />
  Download
</Button>
```

### Badges and Status Pills

```typescript
// ID badge
<span className="inline-block rounded bg-blue-500/20 px-1.5 py-0.5 text-xs font-medium text-blue-400 border border-blue-500/25">
  KB-001
</span>

// Category pill
<span className="inline-block rounded-full bg-zinc-800 px-2 py-0.5 text-xs text-zinc-400">
  network
</span>

// Priority colors
const PRIORITY_STYLES = {
  low:      { bg: 'bg-green-500/15', text: 'text-green-400', border: 'border-green-500/25' },
  medium:   { bg: 'bg-amber-500/15', text: 'text-amber-400', border: 'border-amber-500/25' },
  high:     { bg: 'bg-red-500/15',   text: 'text-red-400',   border: 'border-red-500/25' },
  critical: { bg: 'bg-red-600/20',   text: 'text-red-300',   border: 'border-red-600/30' },
}

// Severity dot
<span className="inline-block h-2 w-2 rounded-full bg-amber-400" />
```

### Grid Layout

```typescript
// Details grid (3 columns)
<div className="grid grid-cols-3 gap-3">
  <div>
    <span className="block text-xs font-medium text-zinc-400 mb-0.5">Category</span>
    <span className="text-sm text-zinc-200 capitalize">{category}</span>
  </div>
  <div>...</div>
  <div>...</div>
</div>
```

### Icons

Import from `lucide-react`. Common icons used in envelopes:
- `Search`, `BookOpen` — search/results
- `Ticket`, `CheckCircle`, `AlertCircle` — status
- `Loader2` — loading spinner (use with `animate-spin`)
- `Download`, `Info` — actions
- `ChevronDown`, `ChevronRight` — expandable sections
- `Lightbulb`, `MinusCircle` — feedback/forms

## Complete Examples

### Example 1: KBResultCard (Search Results with Feedback)

Data schema:

```typescript
interface KBArticle {
  id: string
  title: string
  category: string
  severity: string
  tags?: string[]
  body?: string
  rank?: number
  confidence?: string
  source?: string
}

interface KBResultData {
  results?: KBArticle[]
  articles?: KBArticle[]
  query: string
}
```

Envelope JSON the agent would emit:

```json
{
  "kind": "envelope",
  "version": 1,
  "type": "kb-result",
  "data": {
    "query": "VPN not connecting",
    "results": [
      {
        "id": "KB-042",
        "title": "VPN Connection Troubleshooting",
        "category": "vpn",
        "severity": "medium",
        "body": "## Steps\n1. Restart VPN client...",
        "source": "helix"
      }
    ]
  }
}
```

Features: expandable article cards (first expanded by default), severity dots, source badges, "Did this help?" feedback buttons, fallback to ticket creation flow.

### Example 2: TicketFormCard (Interactive Form)

Data schema:

```typescript
interface TicketFormData {
  prefilled?: {
    title?: string
    description?: string
    category?: string
    priority?: string
    steps_tried?: string
  }
  categories: string[]
}
```

Envelope JSON:

```json
{
  "kind": "envelope",
  "version": 1,
  "type": "ticket-form",
  "data": {
    "categories": ["network", "access", "vpn", "jira", "general"],
    "prefilled": {
      "title": "VPN not connecting after update",
      "description": "After the latest Windows update, GlobalProtect VPN fails to connect.",
      "category": "vpn"
    }
  }
}
```

Features: pre-filled fields from conversation, category dropdown, priority radio buttons, form validation, submit calls `/api/plugins/tickets` directly, success state with ticket ID and download button.

### Example 3: TicketConfirmationCard (Read-Only Display)

Data schema:

```typescript
interface TicketConfirmationData {
  ticket: {
    id: string
    title: string
    description: string
    category: string
    priority: string
    status: string
    requester: string
    routing: string
    created_at: string
  }
}
```

Features: read-only display, no `onSendMessage` needed (no interaction), priority badge with color coding, download button, production-note disclaimer.

### Example 4: ResolutionCaptureCard (Feedback Form)

Data schema:

```typescript
interface ResolutionCaptureData {
  ticket_id?: string
  issue_summary?: string
  categories: string[]
}
```

Features: "What Fixed It" textarea (required), category dropdown, time spent dropdown, "Create KB article?" checkbox, submit/skip buttons, three states (idle/submitted/skipped).

## Checklist: Building a New Envelope

1. Define the **data schema** — what fields does the component need?
2. Write the **React component** following the props pattern (`data` + `onSendMessage?`)
3. Handle **all states**: idle, loading, success, error (and possibly skipped)
4. Add to **plugin.yaml** under `registers.envelopes`
5. Run **codegen**: `cd ui && npm run generate:plugins`
6. Update the **agent system prompt** with instructions to emit the envelope JSON
7. Test: verify the agent emits valid JSON, the component renders, and `onSendMessage` works

## Key Source Paths (Conduit repo)

| What | Path |
|------|------|
| EnvelopeRenderer | `ui/src/components/chat/envelopes/EnvelopeRenderer.tsx` |
| Generated registry | `ui/src/generated/plugin-envelopes.ts` |
| Codegen script | `scripts/generate-plugin-imports.mjs` |
| KBResultCard | `plugins/support-ticket/ui/KBResultCard.tsx` |
| TicketFormCard | `plugins/support-ticket/ui/TicketFormCard.tsx` |
| TicketConfirmationCard | `plugins/support-ticket/ui/TicketConfirmationCard.tsx` |
| ResolutionCaptureCard | `plugins/support-ticket/ui/ResolutionCaptureCard.tsx` |
| Button component | `ui/src/components/ui/Button.tsx` |
