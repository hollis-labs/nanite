/**
 * SystemPromptsViewer — CW-20260421-0005
 *
 * Read-only viewer for all LLM-facing system prompts in the Nanite harness.
 * Gated behind developer_mode — standard users never see this panel.
 *
 * V1: static catalogue of code-owned prompts with file:line references.
 * No editing. Variable interpolation / composition pipeline preserved as-is.
 */

import { ChevronDown, ChevronRight, FileCode } from "lucide-react";
import { useState } from "react";

// ─── Static prompt catalogue ────────────────────────────────────────────────
// Cross-referenced against inbox/nanite-prompts-snapshot-2026-04-19.md and
// docs/agent-role-prompt-catalog.md (M1 audit).

export interface SystemPromptEntry {
  /** Display name */
  name: string;
  /** Unique slug (matches Go slug where applicable) */
  slug: string;
  /** How it enters the composition pipeline */
  kind: "builtin-embed" | "builtin-go-var" | "builtin-db-seed" | "builtin-yaml";
  /** Scope label from store or doc */
  scope: string;
  /** Priority in ComposeFpromptForAgent (undefined = not injected via compose) */
  priority?: number;
  /** Source file + line reference */
  source: string;
  /** The default (original) prompt text */
  defaultTemplate: string;
  /** Interpolation variables declared (empty if none) */
  variables: string[];
  /** Free-text note for the viewer */
  note?: string;
}

/** Default/original text for prompts that have variables — shown verbatim. */
const CATALOGUE: SystemPromptEntry[] = [
  // ── 1. Base Chat Agent (Go embed) ──────────────────────────────────────
  {
    name: "Base Chat Agent",
    slug: "default-builtin",
    kind: "builtin-embed",
    scope: "system",
    source: "internal/agent/builtin/default.md",
    variables: [],
    note:
      "Embedded via //go:embed. Seeded to DB at container init. Active fallback for every " +
      "session with no agent assigned or when using the built-in default agent. This is the " +
      "primary effective system prompt for standard usage.",
    defaultTemplate: `---
name: Default
slug: default
description: General-purpose chat agent
icon: chat
---
You are a helpful AI assistant embedded in the Nanite chat harness. You have access to tools — file system, HTTP, math, MCP servers, and Nanite's own self-tools — and your job is to use them precisely and ground everything you claim in what they actually returned.

## Grounding (non-negotiable)

- If the user asks about live state (tasks, projects, sprints, files, configs, sessions), **call the tool that returns that data** before answering. Do not answer from memory or guess.
- If you did not fetch the data this turn, say so in plain text. Do **not** render a \`report-card\` / \`document-viewer\` / other envelope card from data you don't have — those cards carry visual authority the user will trust, so empty/fabricated cards are worse than a plain "I'd need to call X to answer that" reply.
- When you call \`nanite_show_report\` or \`nanite_show_document\`, build the \`sources\` array as you go from each tool call's \`tool_use_id\`. Only cite calls you actually made this turn.
- **Count, don't estimate.** When you have the data, count it — exact numbers, not "~75%" or "about 40". If a tool returned a paginated result and you need a total, paginate or request a higher limit; don't approximate from the first page.
- **Use real IDs.** When a tool takes an ID, pass an ID that was returned by a prior tool call in THIS turn. Never pattern-match an ID shape and guess — ID schemes are tool-specific and guessed IDs fail with "not found". If you don't have a real ID yet, call the list/search tool first.
- **Never extrapolate list rows.** When rendering a list of N items, every row must come from text you actually retrieved. If the retrieved slice contains fewer than N items, paginate until you have N — or render what you retrieved and say "showing K of N".
- **Honor filters at the tool level.** If the user asks for a filtered view, either call the tool with those filter parameters, or retrieve the unfiltered list and filter in memory — and say "filtered from N total".
- **Ask before you fabricate.** When retrieved data is incomplete, conflicting, or too sparse to answer the user's actual question, stop and ask.

## Tool cadence

- **Glob/search before read.** Running \`dev_read\` on a path you haven't confirmed exists wastes a round-trip.
- **Use the cache pointer.** Large tool results end with \`tool_result://<ULID>\`. Retrieve slices with \`fetch_tool_result\` or regex with \`search_tool_result\` — don't re-invoke the source tool.
- **Stop when you have the answer.** More tool calls do not make answers more trustworthy; irrelevant calls dilute the grounding.
- **Parallelize independent calls.** If two lookups don't depend on each other, request them in the same turn.

## Style

- Be direct. Match the user's terseness — no ceremony, no trailing summaries, no "I hope this helps."
- Use Markdown for structure when it earns its keep (lists, code, tables). Prose for everything else.
- When the user is clearly capturing rather than asking, acknowledge briefly and don't over-explain.
- Do not narrate your tool plan ("I'll now call X then Y") unless the user asked for it.

## Judgment

- If unsure about scope, ask one pointed question before running a long tool chain.
- For destructive or externally-visible actions (deletes, pushes, posts, emails), confirm first.
- If a tool returns an error, acknowledge it honestly — don't paper over failures with fabricated content.`,
  },

  // ── 2. Platform Capabilities / PlatformPromptTemplate (Go var) ──────────
  {
    name: "Platform Capabilities",
    slug: "platform-capabilities",
    kind: "builtin-go-var",
    scope: "platform",
    priority: 5,
    source: "internal/store/prompt_templates.go:292 (PlatformPromptTemplate var)",
    variables: [],
    note:
      "Go var — NOT stored in DB. Injected by ComposePromptForAgent() at priority 5 for every " +
      "agent that has ≥1 DB template assigned. Zero-consumer by default (no agent has templates " +
      "assigned in a fresh install). Contains Mentat/Fragments Engine identity — stale platform text. " +
      "Flagged for replacement in B5-DF.",
    defaultTemplate: `## Mentat — Fragments Engine Operator

You are Mentat, the intelligent operator of the Fragments Engine platform. You are not a general-purpose chatbot with tools bolted on — you are the strategist, planner, and executor for the user's projects and work. You have deep, native knowledge of the platform and its services.

You help the user plan, create, design, build, write, and execute. You manage their projects, tasks, knowledge, and automation directly. You don't fumble through tool discovery — you know your tools the way a craftsman knows their workshop.

### Your Core Services

You operate four integrated services. These are not optional plugins — they are part of who you are.

**Engine — Project & Task Management**
**Vanta Conduit — Memory & Context**
**Nanite — Inbox & Capture**
**Hadron — Automation & Pipelines**

[Full template: internal/store/prompt_templates.go:297–403]`,
  },

  // ── 3–7. BuiltinPromptTemplates (DB-seeded, priority 10–50) ─────────────
  {
    name: "Base Identity",
    slug: "base-identity",
    kind: "builtin-db-seed",
    scope: "system",
    priority: 10,
    source: "internal/store/prompt_templates.go:241 (BuiltinPromptTemplates[0])",
    variables: ["agent_name", "agent_description"],
    note:
      "Seeded to DB with is_builtin=1. Not auto-assigned to any agent. Consumer count: 0 unless " +
      "explicitly assigned via AgentDetailView.",
    defaultTemplate: `You are {{agent_name}}, {{agent_description}}.`,
  },
  {
    name: "Workspace Context",
    slug: "workspace-context",
    kind: "builtin-db-seed",
    scope: "context",
    priority: 20,
    source: "internal/store/prompt_templates.go:249 (BuiltinPromptTemplates[1])",
    variables: ["workspace_name", "workspace_description"],
    note: "Seeded to DB with is_builtin=1. Not auto-assigned.",
    defaultTemplate: `You are operating within the workspace "{{workspace_name}}": {{workspace_description}}.`,
  },
  {
    name: "Project Context",
    slug: "project-context",
    kind: "builtin-db-seed",
    scope: "context",
    priority: 30,
    source: "internal/store/prompt_templates.go:257 (BuiltinPromptTemplates[2])",
    variables: ["project_name", "project_description"],
    note: "Seeded to DB with is_builtin=1. Not auto-assigned.",
    defaultTemplate: `Current project: {{project_name}}. {{project_description}}`,
  },
  {
    name: "Mode Addendum",
    slug: "mode-addendum",
    kind: "builtin-db-seed",
    scope: "mode",
    priority: 40,
    source: "internal/store/prompt_templates.go:265 (BuiltinPromptTemplates[3])",
    variables: ["mode_addendum"],
    note:
      "Seeded to DB with is_builtin=1. Resolves to empty string when no mode is set (skipped " +
      "by ComposePromptForAgent). Not auto-assigned.",
    defaultTemplate: `{{mode_addendum}}`,
  },
  {
    name: "Tool Awareness",
    slug: "tool-awareness",
    kind: "builtin-db-seed",
    scope: "skill",
    priority: 50,
    source: "internal/store/prompt_templates.go:273 (BuiltinPromptTemplates[4])",
    variables: ["skill_list"],
    note: "Seeded to DB with is_builtin=1. Not auto-assigned.",
    defaultTemplate: `You have access to the following skills and their tools:
{{skill_list}}

Use these tools when appropriate to accomplish tasks. Each skill provides specific capabilities that you can invoke.`,
  },

  // ── 8. Worker agent system prompt (YAML config) ─────────────────────────
  {
    name: "Worker Agent",
    slug: "worker-builtin-yaml",
    kind: "builtin-yaml",
    scope: "system",
    source: "config/agents/worker.yaml (system_prompt field)",
    variables: [],
    note:
      "Embedded in YAML config. Loaded by the agent profile creation path for the built-in " +
      "worker agent. References 'Fragments Engine' — Mentat-era copy. Scheduled for update " +
      "once B5-DF resolves the platform identity question.",
    defaultTemplate: `[Loaded from config/agents/worker.yaml — system_prompt field.\nView file for full text.]`,
  },
];

// ─── Kind metadata ────────────────────────────────────────────────────────
const KIND_LABEL: Record<SystemPromptEntry["kind"], string> = {
  "builtin-embed": "Go embed",
  "builtin-go-var": "Go var",
  "builtin-db-seed": "DB seed",
  "builtin-yaml": "YAML config",
};

const KIND_TONE: Record<SystemPromptEntry["kind"], string> = {
  "builtin-embed": "bg-status-ok/10 text-status-ok border-status-ok/30",
  "builtin-go-var": "bg-status-warn/10 text-status-warn border-status-warn/30",
  "builtin-db-seed": "bg-brand/10 text-brand border-brand/30",
  "builtin-yaml": "bg-status-info/10 text-status-info border-status-info/30",
};

const SCOPE_TONE: Record<string, string> = {
  system: "bg-status-info/10 text-status-info border-status-info/30",
  context: "bg-brand/10 text-brand border-brand/30",
  mode: "bg-status-ok/10 text-status-ok border-status-ok/30",
  skill: "bg-status-warn/10 text-status-warn border-status-warn/30",
  platform: "bg-status-error/10 text-status-error border-status-error/30",
};

// ─── PromptCard ────────────────────────────────────────────────────────────
function PromptCard({ entry }: { entry: SystemPromptEntry }) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden">
      {/* Header row */}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="w-full flex items-start gap-3 px-4 py-3.5 text-left hover:bg-surface transition-colors"
      >
        <span className="mt-0.5 shrink-0 text-fg-secondary">
          {expanded ? (
            <ChevronDown style={{ width: 14, height: 14 }} />
          ) : (
            <ChevronRight style={{ width: 14, height: 14 }} />
          )}
        </span>

        <div className="flex-1 min-w-0">
          <div className="flex flex-wrap items-center gap-2 mb-1.5">
            <span className="text-sm font-semibold text-fg">{entry.name}</span>
            <span
              className={`inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium border ${KIND_TONE[entry.kind]}`}
            >
              {KIND_LABEL[entry.kind]}
            </span>
            <span
              className={`inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium border ${SCOPE_TONE[entry.scope] ?? "bg-surface text-fg-muted border-border-subtle"}`}
            >
              {entry.scope}
            </span>
            {entry.priority !== undefined && (
              <span className="text-[11px] text-fg-faint font-mono">P{entry.priority}</span>
            )}
          </div>

          {/* Source reference */}
          <div className="flex items-center gap-1.5 text-[11px] text-fg-faint font-mono">
            <FileCode style={{ width: 11, height: 11 }} className="shrink-0" />
            <span className="truncate">{entry.source}</span>
          </div>
        </div>
      </button>

      {/* Expanded body */}
      {expanded && (
        <div className="border-t border-border-subtle">
          {/* Note */}
          {entry.note && (
            <div className="px-4 py-3 border-b border-border-subtle bg-surface/40">
              <p className="text-[12px] text-fg-muted leading-relaxed">{entry.note}</p>
            </div>
          )}

          {/* Variables */}
          {entry.variables.length > 0 && (
            <div className="px-4 py-3 border-b border-border-subtle flex items-center gap-2 flex-wrap">
              <span className="text-[11px] font-medium text-fg-secondary mr-1">Variables:</span>
              {entry.variables.map((v) => (
                <code
                  key={v}
                  className="text-[11px] px-1.5 py-0.5 rounded bg-surface border border-border-subtle font-mono text-fg-muted"
                >
                  {`{{${v}}}`}
                </code>
              ))}
            </div>
          )}

          {/* Default template */}
          <div className="px-4 py-3">
            <div className="flex items-center gap-2 mb-2">
              <span className="text-[11px] font-semibold text-fg-secondary uppercase tracking-wide">
                Default (original)
              </span>
              <span className="text-[10px] text-fg-faint">read-only</span>
            </div>
            <pre className="text-[11px] text-fg-muted font-mono leading-relaxed whitespace-pre-wrap break-words rounded-lg bg-surface border border-border-subtle px-3 py-2.5 overflow-x-auto">
              {entry.defaultTemplate}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}

// ─── SystemPromptsViewer (public export) ─────────────────────────────────
export function SystemPromptsViewer() {
  return (
    <div className="space-y-5">
      {/* Header */}
      <div>
        <h2 className="text-base font-semibold text-fg mb-1">System Prompts</h2>
        <p className="text-[13px] text-fg-muted leading-relaxed">
          All LLM-facing prompts currently active in the Nanite harness. Read-only — editing is not
          yet supported. Cross-referenced against{" "}
          <code className="text-[11px] font-mono bg-surface border border-border-subtle px-1 py-0.5 rounded">
            inbox/nanite-prompts-snapshot-2026-04-19.md
          </code>{" "}
          and{" "}
          <code className="text-[11px] font-mono bg-surface border border-border-subtle px-1 py-0.5 rounded">
            docs/agent-role-prompt-catalog.md
          </code>
          .
        </p>
      </div>

      {/* Kind legend */}
      <div className="flex flex-wrap gap-2">
        {(Object.keys(KIND_LABEL) as SystemPromptEntry["kind"][]).map((k) => (
          <span
            key={k}
            className={`inline-flex items-center px-2.5 py-1 rounded-full text-[11px] font-medium border ${KIND_TONE[k]}`}
          >
            {KIND_LABEL[k]}
          </span>
        ))}
        <span className="text-[11px] text-fg-faint self-center ml-1">
          {CATALOGUE.length} prompts total
        </span>
      </div>

      {/* Prompt cards */}
      <div className="space-y-2">
        {CATALOGUE.map((entry) => (
          <PromptCard key={entry.slug} entry={entry} />
        ))}
      </div>

      {/* Footer note */}
      <div className="rounded-lg border border-border-subtle bg-surface px-4 py-3">
        <p className="text-[11px] text-fg-faint leading-relaxed">
          <strong className="text-fg-muted">Composition pipeline:</strong> Agents with ≥1 assigned
          DB templates use <code className="font-mono">ComposePromptForAgent()</code> (template
          path) which prepends Platform Capabilities (P5) then appends DB templates in priority
          order. Agents with no assigned templates use the legacy path:{" "}
          <code className="font-mono">assembleSystemPrompt()</code> with{" "}
          <code className="font-mono">agent.SystemPrompt</code> directly. In the default install, no
          agents have templates assigned — the Base Chat Agent is the active prompt for all
          sessions.
        </p>
      </div>
    </div>
  );
}
