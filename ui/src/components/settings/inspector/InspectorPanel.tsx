/**
 * InspectorPanel — I1 (CW-20260426-0004)
 *
 * Dev-mode per-turn context inspector. Surfaces slot snapshots, LLM messages,
 * broker decisions, and tool calls for every chat turn within a session.
 *
 * Gated behind developer_mode — never rendered for standard users.
 */

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { ScopeChip } from "@/components/work/ScopeChip";
import { useInspectorTurn, useInspectorTurns } from "@/hooks/useInspector";
import { api } from "@/lib/api";
import type {
  InspectorBrokerDecision,
  InspectorLLMMessageRecord,
  InspectorRemindersRecord,
  InspectorSlotSnapshot,
  InspectorToolCallRecord,
  InspectorTurnSnapshot,
} from "@/lib/types";
import { usePendingModeSuggestion } from "@/stores/useChatStore";

// ─── Sub-components ──────────────────────────────────────────────────────────

function PanelCard({
  title,
  children,
  className = "",
}: {
  title: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden ${className}`}
    >
      <div className="px-4 py-2.5 border-b border-border-subtle">
        <h4 className="text-[11px] uppercase tracking-wider text-fg-muted font-medium">{title}</h4>
      </div>
      <div className="p-3">{children}</div>
    </div>
  );
}

function TrafficDot({ colour }: { colour: "green" | "yellow" | "red" | string }) {
  const bg =
    colour === "green" ? "bg-green-500" : colour === "yellow" ? "bg-yellow-400" : "bg-red-500";
  return <span className={`inline-block w-2 h-2 rounded-full mr-1.5 ${bg}`} />;
}

function TokenBadge({ count }: { count: number }) {
  return (
    <span className="text-[10px] font-mono text-fg-muted bg-bg px-1 py-0.5 rounded border border-border-subtle ml-1">
      {count.toLocaleString()}t
    </span>
  );
}

// ─── Slot grid ───────────────────────────────────────────────────────────────

function SlotGrid({ slots, reveal }: { slots: InspectorSlotSnapshot[]; reveal: boolean }) {
  if (!slots || slots.length === 0) {
    return (
      <EmptyProducer label="No slot data recorded — producer not yet wired or turn is still in flight." />
    );
  }

  return (
    <div className="grid grid-cols-2 gap-2">
      {slots.map((slot) => (
        <div
          key={slot.name}
          className="rounded-lg border border-border-subtle bg-bg p-2.5 text-[11px]"
        >
          <div className="flex items-center justify-between mb-1">
            <span className="font-semibold text-fg capitalize">{slot.name}</span>
            <span className="flex items-center">
              <TrafficDot colour={slot.traffic_light} />
              <TokenBadge count={slot.tokens} />
              {slot.cached && (
                <span className="ml-1 text-[9px] text-green-600 font-medium">cached</span>
              )}
            </span>
          </div>
          {slot.tokens > 0 && (
            <div className="mt-1">
              {slot.sensitive && !reveal ? (
                <span className="text-fg-muted italic text-[10px]">
                  [sensitive — toggle Reveal to show]
                </span>
              ) : (
                <pre className="text-[10px] text-fg-muted whitespace-pre-wrap break-words max-h-24 overflow-auto font-mono">
                  {slot.content.slice(0, 500)}
                  {slot.content.length > 500 ? "…" : ""}
                </pre>
              )}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

// ─── LLM Messages ────────────────────────────────────────────────────────────

function LLMMessageList({ msgs }: { msgs: InspectorLLMMessageRecord[] }) {
  if (!msgs || msgs.length === 0) {
    return <EmptyProducer label="No LLM messages recorded." />;
  }

  return (
    <div className="space-y-1.5">
      {msgs.map((msg, i) => (
        <div key={i} className="rounded-lg border border-border-subtle bg-bg p-2 text-[11px]">
          <div className="flex items-center gap-2 mb-1">
            <Badge variant="outline" className="text-[9px] uppercase px-1.5 py-0">
              {msg.role}
            </Badge>
            {msg.classification && (
              <span className="text-[9px] text-fg-muted">{msg.classification}</span>
            )}
            <TokenBadge count={msg.tokens} />
          </div>
          <pre className="text-[10px] text-fg-muted whitespace-pre-wrap break-words max-h-20 overflow-auto font-mono">
            {msg.content.slice(0, 400)}
            {msg.content.length > 400 ? "…" : ""}
          </pre>
        </div>
      ))}
    </div>
  );
}

// ─── Broker Decisions ────────────────────────────────────────────────────────

function BrokerDecisionList({ decisions }: { decisions: InspectorBrokerDecision[] }) {
  if (!decisions || decisions.length === 0) {
    return (
      <EmptyProducer label="No broker decisions recorded (no request_tools calls this turn)." />
    );
  }

  const outcomeColour = (outcome: string) => {
    switch (outcome) {
      case "loaded":
        return "bg-green-100 text-green-800 border-green-300";
      case "empty":
        return "bg-yellow-100 text-yellow-800 border-yellow-300";
      case "halted":
        return "bg-red-100 text-red-800 border-red-300";
      case "reflected":
        return "bg-blue-100 text-blue-800 border-blue-300";
      default:
        return "bg-bg-elevated text-fg-muted border-border-subtle";
    }
  };

  return (
    <div className="space-y-1.5">
      {decisions.map((d, i) => (
        <div key={i} className="rounded-lg border border-border-subtle bg-bg p-2 text-[11px]">
          <div className="flex items-center gap-2 mb-1 flex-wrap">
            <span
              className={`rounded border px-1.5 py-0.5 text-[9px] font-semibold uppercase ${outcomeColour(d.outcome)}`}
            >
              {d.outcome}
            </span>
            <span className="text-fg-muted text-[10px]">call #{d.total_calls}</span>
            {d.consecutive_empty > 0 && (
              <span className="text-yellow-600 text-[10px]">
                consecutive_empty={d.consecutive_empty}
              </span>
            )}
            {d.loaded_count > 0 && (
              <span className="text-green-700 text-[10px]">+{d.loaded_count} loaded</span>
            )}
          </div>
          <div className="text-fg text-[10px] mb-1 font-medium truncate">
            intent: <span className="font-normal text-fg-muted">{d.intent || "(none)"}</span>
          </div>
          {d.selected_tools && d.selected_tools.length > 0 && (
            <div className="flex flex-wrap gap-1 mt-1">
              {d.selected_tools.map((t) => (
                <span
                  key={t}
                  className="text-[9px] bg-bg-elevated border border-border-subtle rounded px-1 py-0.5 font-mono"
                >
                  {t}
                </span>
              ))}
            </div>
          )}
          {d.reflection_query && (
            <div className="mt-1 text-[10px] text-blue-700 italic">
              reflection: {d.reflection_query.slice(0, 200)}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

// ─── Tool Calls ──────────────────────────────────────────────────────────────

function ToolCallList({ calls }: { calls: InspectorToolCallRecord[] }) {
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  if (!calls || calls.length === 0) {
    return <EmptyProducer label="No tool calls recorded this turn." />;
  }

  return (
    <div className="space-y-1.5">
      {calls.map((call, i) => (
        <div key={i} className="rounded-lg border border-border-subtle bg-bg text-[11px]">
          <button
            className="w-full flex items-center gap-2 p-2 text-left hover:bg-bg-elevated/50 transition-colors"
            onClick={() =>
              setExpanded((prev) => {
                const next = new Set(prev);
                next.has(i) ? next.delete(i) : next.add(i);
                return next;
              })
            }
          >
            {call.is_error ? <TrafficDot colour="red" /> : <TrafficDot colour="green" />}
            <span className="font-mono font-semibold text-fg">{call.name}</span>
            <span className="text-fg-muted text-[10px]">{call.latency_ms}ms</span>
            <span className="ml-auto text-[10px] text-fg-muted">{expanded.has(i) ? "▲" : "▼"}</span>
          </button>
          {expanded.has(i) && (
            <div className="px-2 pb-2 space-y-1.5 border-t border-border-subtle pt-2">
              <div>
                <span className="text-[9px] uppercase text-fg-muted font-medium">Arguments</span>
                <pre className="text-[10px] font-mono text-fg-muted mt-0.5 max-h-24 overflow-auto">
                  {call.arguments}
                </pre>
              </div>
              <div>
                <span className="text-[9px] uppercase text-fg-muted font-medium">Result</span>
                <pre className="text-[10px] font-mono text-fg-muted mt-0.5 max-h-32 overflow-auto whitespace-pre-wrap">
                  {call.result.slice(0, 1000)}
                  {call.result.length > 1000 ? "…" : ""}
                </pre>
              </div>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

// ─── Empty producer placeholder ──────────────────────────────────────────────

function EmptyProducer({ label }: { label: string }) {
  return <p className="text-[11px] text-fg-muted italic py-2">{label}</p>;
}

// ─── Turn detail view ────────────────────────────────────────────────────────

type TurnTab = "slots" | "llm" | "broker" | "tools" | "meta";

function TurnDetail({
  sessionId,
  turnId,
  reveal,
}: {
  sessionId: string;
  turnId: string;
  reveal: boolean;
}) {
  const [activeTab, setActiveTab] = useState<TurnTab>("slots");
  const { data: snap, isLoading } = useInspectorTurn(sessionId, turnId);

  if (isLoading) {
    return <Skeleton className="h-48 w-full" />;
  }
  if (!snap) {
    return <p className="text-[11px] text-fg-muted italic">Turn not found.</p>;
  }

  const tabs: { id: TurnTab; label: string }[] = [
    { id: "slots", label: "Slots" },
    { id: "llm", label: "LLM Messages" },
    { id: "broker", label: "Broker" },
    { id: "tools", label: "Tool Calls" },
    { id: "meta", label: "Meta" },
  ];

  return (
    <div className="space-y-3">
      {/* Turn header */}
      <div className="flex items-center gap-3 text-[11px]">
        <span className="font-semibold text-fg">Turn #{snap.turn_id}</span>
        {snap.scope_tier && (
          <Badge variant="outline" className="text-[9px]">
            {snap.scope_tier}
          </Badge>
        )}
        <span className="text-fg-muted ml-auto">
          {new Date(snap.started_at).toLocaleTimeString()}
        </span>
      </div>

      {/* Tabs */}
      <div className="flex gap-0.5 border-b border-border-subtle pb-px">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setActiveTab(t.id)}
            className={`px-3 py-1.5 text-[11px] rounded-t transition-colors ${
              activeTab === t.id
                ? "bg-bg-elevated text-fg font-medium border border-border-subtle border-b-bg-elevated -mb-px"
                : "text-fg-muted hover:text-fg"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <ScrollArea className="h-[480px]">
        {activeTab === "slots" && <SlotGrid slots={snap.slots ?? []} reveal={reveal} />}
        {activeTab === "llm" && <LLMMessageList msgs={snap.llm_messages ?? []} />}
        {activeTab === "broker" && <BrokerDecisionList decisions={snap.broker_decisions ?? []} />}
        {activeTab === "tools" && <ToolCallList calls={snap.tool_calls ?? []} />}
        {activeTab === "meta" && (
          <div className="space-y-2 text-[11px]">
            {/* F2 (CW-20260429-0002) — SlotMode + pending mode suggestion. */}
            <ModeSection sessionId={sessionId} slots={snap.slots ?? []} />
            {snap.strategy ? (
              <PanelCard title="Strategy">
                <dl className="grid grid-cols-2 gap-1 text-[11px]">
                  <dt className="text-fg-muted">Max turns</dt>
                  <dd>{snap.strategy.max_turns}</dd>
                  {snap.strategy.reflex_match_id && (
                    <>
                      <dt className="text-fg-muted">Reflex</dt>
                      <dd className="font-mono">{snap.strategy.reflex_match_id}</dd>
                    </>
                  )}
                </dl>
              </PanelCard>
            ) : (
              <EmptyProducer label="Strategy — no signal yet, producer not wired." />
            )}
            {snap.loop_status ? (
              <PanelCard title="Loop Detection">
                <p>
                  Detected:{" "}
                  <span className={snap.loop_status.detected ? "text-red-600" : "text-green-600"}>
                    {snap.loop_status.detected ? "yes" : "no"}
                  </span>
                </p>
              </PanelCard>
            ) : (
              <EmptyProducer label="Loop status — no signal yet (I2 not wired)." />
            )}
            <RemindersSection reminders={snap.reminders} />
          </div>
        )}
      </ScrollArea>
    </div>
  );
}

// ─── Mode section (F2, CW-20260429-0002) ────────────────────────────────────
//
// Surfaces SlotMode and SlotAgent on separate labeled lines so debugging
// "why is the agent acting like a planner?" is one inspector lookup. The
// pendingModeSuggestion row reflects the same B2 SSE signal the mode chip
// uses (read straight from useChatStore — no separate subscription needed).

function ModeSection({ sessionId, slots }: { sessionId: string; slots: InspectorSlotSnapshot[] }) {
  // Live-updated session mode (PATCH /api/sessions/{id}/mode invalidates this
  // query, same key the ChatHeader chip uses — staying on a single source).
  const { data: sessionMode } = useQuery({
    queryKey: ["session-mode", sessionId],
    queryFn: () => api.getSessionMode(sessionId),
    enabled: !!sessionId,
  });

  // Same SSE-fed signal the mode chip subscribes to. No new subscription.
  const pendingSuggestion = usePendingModeSuggestion(sessionId);

  const slotByName = new Map(slots.map((s) => [s.name, s]));
  const modeSlot = slotByName.get("mode");
  const agentSlot = slotByName.get("agent");

  return (
    <PanelCard title="Mode">
      <dl className="grid grid-cols-[120px_1fr] gap-x-2 gap-y-1 text-[11px]">
        {/* SlotAgent — kept on its own line. */}
        <dt className="text-fg-muted">SlotAgent</dt>
        <dd className="text-fg">
          {agentSlot ? (
            <>
              <span className="font-mono text-[10px] text-fg-muted">
                {agentSlot.tokens.toLocaleString()}t
              </span>
              {agentSlot.cached && <span className="ml-1 text-[10px] text-green-600">cached</span>}
            </>
          ) : (
            <span className="italic text-fg-muted">no slot data</span>
          )}
        </dd>

        {/* SlotMode — split out from SlotAgent (B1 introduced the slot but the
            inspector previously lumped it under Agent). */}
        <dt className="text-fg-muted">SlotMode</dt>
        <dd className="text-fg">
          {modeSlot ? (
            <>
              <span className="font-mono text-[10px] text-fg-muted">
                {modeSlot.tokens.toLocaleString()}t
              </span>
              {modeSlot.cached && <span className="ml-1 text-[10px] text-green-600">cached</span>}
              {modeSlot.tokens === 0 && (
                <span className="ml-1 italic text-fg-muted">empty (no addendum this turn)</span>
              )}
            </>
          ) : (
            <span className="italic text-fg-muted">no slot data</span>
          )}
        </dd>

        {/* Active session mode — resolved via current_mode_id → modes.id. */}
        <dt className="text-fg-muted">Active session mode</dt>
        <dd className="text-fg font-mono">
          {sessionMode ? (
            <>
              {sessionMode.slug}
              <span className="ml-1 text-[10px] text-fg-muted">({sessionMode.id})</span>
            </>
          ) : (
            <span className="italic text-fg-muted">none (legacy AgentMode fallback)</span>
          )}
        </dd>

        {/* Pending classifier suggestion — B2 (CW-20260428-0010). */}
        <dt className="text-fg-muted">Pending Mode Suggestion</dt>
        <dd className="text-fg">
          {pendingSuggestion ? (
            <div className="space-y-0.5">
              <div className="font-mono text-[10px]">
                {pendingSuggestion.current} → {pendingSuggestion.suggested}
              </div>
              <div className="text-[10px] text-fg-muted">
                confidence:{" "}
                <span className="font-mono">
                  {(pendingSuggestion.confidence * 100).toFixed(0)}%
                </span>
              </div>
              {pendingSuggestion.signals && pendingSuggestion.signals.length > 0 && (
                <div className="text-[10px] text-fg-muted">
                  reason: <span className="text-fg">{pendingSuggestion.signals.join(", ")}</span>
                </div>
              )}
            </div>
          ) : (
            <span className="italic text-fg-muted">no pending suggestion</span>
          )}
        </dd>
      </dl>
    </PanelCard>
  );
}

// ─── Reminders section (J11, CW-20260426-0009) ───────────────────────────────

function RemindersSection({ reminders }: { reminders?: InspectorRemindersRecord }) {
  const hasData =
    (reminders?.set_this_turn && reminders.set_this_turn.length > 0) ||
    (reminders?.fired_this_turn && reminders.fired_this_turn.length > 0);

  if (!hasData) {
    return <EmptyProducer label="Reminders — none set or fired this turn." />;
  }

  return (
    <PanelCard title="Reminders">
      <div className="space-y-2 text-[11px]">
        {reminders?.fired_this_turn && reminders.fired_this_turn.length > 0 && (
          <div>
            <p className="text-[10px] uppercase text-fg-muted font-medium mb-1">Fired this turn</p>
            <div className="space-y-1">
              {reminders.fired_this_turn.map((r) => (
                <div
                  key={r.id}
                  className="rounded border border-success bg-success-muted px-2 py-1 text-fg"
                >
                  <span className="font-medium">↑ </span>
                  {r.text}
                  {r.scope && <ScopeChip scope={r.scope} className="ml-2" />}
                  <span className="ml-2 text-[9px] text-fg-muted font-mono">{r.trigger_json}</span>
                </div>
              ))}
            </div>
          </div>
        )}
        {reminders?.set_this_turn && reminders.set_this_turn.length > 0 && (
          <div>
            <p className="text-[10px] uppercase text-fg-muted font-medium mb-1">Set this turn</p>
            <div className="space-y-1">
              {reminders.set_this_turn.map((r) => (
                <div
                  key={r.id}
                  className="rounded border border-border-subtle bg-bg px-2 py-1 text-fg-muted"
                >
                  <span className="font-mono text-[9px] text-fg-faint">{r.id}: </span>
                  {r.text}
                  {r.scope && <ScopeChip scope={r.scope} className="ml-2" />}
                  <span className="ml-2 text-[9px] font-mono text-fg-faint">{r.trigger_json}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </PanelCard>
  );
}

// ─── Main panel ──────────────────────────────────────────────────────────────

interface InspectorPanelProps {
  /** Active session ID from the parent. If null, prompts the user to select. */
  sessionId?: string | null;
}

export function InspectorPanel({ sessionId: propSessionId }: InspectorPanelProps = {}) {
  const [sessionId, setSessionId] = useState<string>(propSessionId ?? "");
  const [selectedTurnId, setSelectedTurnId] = useState<string | null>(null);
  const [reveal, setReveal] = useState(false);
  const [filter, setFilter] = useState("");

  const activeSession = propSessionId ?? sessionId;

  const { data, isLoading, error } = useInspectorTurns(activeSession || null, 50);

  const turns = (data?.turns ?? []).filter((t: InspectorTurnSnapshot) => {
    if (!filter) return true;
    const f = filter.toLowerCase();
    return (
      t.turn_id.includes(f) ||
      t.scope_tier?.toLowerCase().includes(f) ||
      t.broker_decisions?.some((d) => d.intent?.toLowerCase().includes(f)) ||
      t.tool_calls?.some((c) => c.name?.toLowerCase().includes(f))
    );
  });

  return (
    <div className="space-y-4 max-w-6xl">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold text-fg">Context Inspector</h3>
          <p className="text-[11px] text-fg-muted mt-0.5">
            Per-turn debug oracle — slots, LLM messages, broker decisions, tool calls.
          </p>
        </div>
        <label className="flex items-center gap-2 text-[11px] text-fg-muted cursor-pointer">
          <input
            type="checkbox"
            checked={reveal}
            onChange={(e) => setReveal(e.target.checked)}
            className="rounded"
          />
          Reveal sensitive
        </label>
      </div>

      {/* Session input when no prop session */}
      {!propSessionId && (
        <div className="flex gap-2">
          <Input
            value={sessionId}
            onChange={(e) => {
              setSessionId(e.target.value);
              setSelectedTurnId(null);
            }}
            placeholder="Session ID"
            className="font-mono text-[11px] h-8 max-w-xs"
          />
        </div>
      )}

      {!activeSession && (
        <p className="text-[11px] text-fg-muted italic">
          Enter a session ID to load its inspector snapshots.
        </p>
      )}

      {activeSession && (
        <div className="grid grid-cols-[280px_1fr] gap-4">
          {/* Turn list */}
          <div className="space-y-2">
            <Input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter turns…"
              className="text-[11px] h-7"
            />
            {isLoading && <Skeleton className="h-32 w-full" />}
            {error && (
              <p className="text-[11px] text-red-500">
                {error instanceof Error ? error.message : String(error)}
              </p>
            )}
            {!isLoading && turns.length === 0 && (
              <p className="text-[11px] text-fg-muted italic">
                No turns recorded. Start a chat turn with developer_mode enabled.
              </p>
            )}
            <ScrollArea className="h-[560px]">
              <div className="space-y-1 pr-2">
                {turns.map((turn: InspectorTurnSnapshot) => (
                  <button
                    key={turn.turn_id}
                    onClick={() => setSelectedTurnId(turn.turn_id)}
                    className={`w-full text-left rounded-lg border px-2.5 py-2 text-[11px] transition-colors ${
                      selectedTurnId === turn.turn_id
                        ? "border-accent bg-accent/10 text-fg"
                        : "border-border-subtle bg-bg hover:bg-bg-elevated text-fg-muted hover:text-fg"
                    }`}
                  >
                    <div className="flex items-center justify-between">
                      <span className="font-semibold">Turn #{turn.turn_id}</span>
                      {turn.scope_tier && (
                        <span className="text-[9px] text-fg-muted">{turn.scope_tier}</span>
                      )}
                    </div>
                    <div className="mt-0.5 flex gap-2 text-[10px] text-fg-muted">
                      <span>{turn.slots?.length ?? 0} slots</span>
                      <span>{turn.tool_calls?.length ?? 0} tools</span>
                      <span>{turn.broker_decisions?.length ?? 0} broker</span>
                    </div>
                    <div className="mt-0.5 text-[9px] text-fg-muted">
                      {new Date(turn.started_at).toLocaleTimeString()}
                    </div>
                  </button>
                ))}
              </div>
            </ScrollArea>
          </div>

          {/* Turn detail */}
          <div className="min-w-0">
            {selectedTurnId ? (
              <TurnDetail sessionId={activeSession} turnId={selectedTurnId} reveal={reveal} />
            ) : (
              <div className="flex items-center justify-center h-48 text-[11px] text-fg-muted italic border border-border-subtle rounded-xl">
                Select a turn on the left to inspect it.
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
