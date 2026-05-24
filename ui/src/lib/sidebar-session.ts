import type { Session } from "@/lib/types";

export type SidebarSessionKind = "api" | "cli" | "durable";

export type SidebarActivityState =
  | "idle"
  | "online"
  | "working"
  | "pending_action"
  | "failed"
  | "halted"
  | "stopped"
  | "archived";

export interface SidebarPresenceMaps {
  activeStreams: ReadonlyMap<string, unknown>;
  pendingTools: ReadonlyMap<string, unknown>;
  cliActiveSessions: ReadonlyMap<string, unknown>;
}

export interface SidebarSessionSummary {
  kind: SidebarSessionKind;
  kindLabel: string;
  metadataLabel: string;
}

export interface SidebarActivitySummary {
  state: SidebarActivityState;
  label: string;
}

export interface SidebarSessionTreeRow {
  session: Session;
  depth: number;
  parentSessionId: string | null;
  rootSessionId: string | null;
  relation: string | null;
  missingParent: boolean;
}

export interface SidebarSessionTreeSections {
  pinnedRows: SidebarSessionTreeRow[];
  unpinnedRows: SidebarSessionTreeRow[];
}

const CLI_PROVIDER_ALIASES = new Set([
  "pty",
  "pty-claude",
  "pty-codex",
  "codex",
  "opencode",
]);
const MAX_VISIBLE_TREE_DEPTH = 2;

export function buildSidebarSessionTree(
  sessions: Session[],
): SidebarSessionTreeRow[] {
  const byID = new Map(sessions.map((session) => [session.id, session]));
  const childrenByParent = new Map<string, Session[]>();
  const topLevel: Session[] = [];
  const visitedChildren = new Set<string>();

  for (const session of sessions) {
    const parentID = session.parent_session_id || null;
    if (!parentID || !byID.has(parentID) || parentID === session.id) {
      topLevel.push(session);
      continue;
    }
    const children = childrenByParent.get(parentID) ?? [];
    children.push(session);
    childrenByParent.set(parentID, children);
  }

  const rows: SidebarSessionTreeRow[] = [];
  const append = (session: Session, depth: number, path: Set<string>) => {
    rows.push(toTreeRow(session, depth, byID));
    if (path.has(session.id)) return;

    const nextPath = new Set(path);
    nextPath.add(session.id);
    for (const child of childrenByParent.get(session.id) ?? []) {
      if (nextPath.has(child.id)) {
        rows.push(toTreeRow(child, 0, byID));
        visitedChildren.add(child.id);
        continue;
      }
      visitedChildren.add(child.id);
      append(child, Math.min(depth + 1, MAX_VISIBLE_TREE_DEPTH), nextPath);
    }
  };

  for (const session of topLevel) {
    append(session, 0, new Set());
  }

  for (const session of sessions) {
    if (
      session.parent_session_id &&
      byID.has(session.parent_session_id) &&
      !visitedChildren.has(session.id)
    ) {
      rows.push(toTreeRow(session, 0, byID));
    }
  }

  return rows;
}

export function buildSidebarSessionTreeSections(
  pinned: Session[],
  visibleUnpinned: Session[],
): SidebarSessionTreeSections {
  const visibleSessions = [...pinned, ...visibleUnpinned];
  const byID = new Map(visibleSessions.map((session) => [session.id, session]));
  const rows = buildSidebarSessionTree(visibleSessions);

  const hasPinnedAncestor = (session: Session): boolean => {
    const visited = new Set<string>();
    let parentID = session.parent_session_id || null;
    while (parentID) {
      if (visited.has(parentID)) return false;
      visited.add(parentID);

      const parent = byID.get(parentID);
      if (!parent) return false;
      if (parent.is_pinned) return true;
      parentID = parent.parent_session_id || null;
    }
    return false;
  };

  return {
    pinnedRows: rows.filter(
      (row) => row.session.is_pinned || hasPinnedAncestor(row.session),
    ),
    unpinnedRows: rows.filter(
      (row) => !row.session.is_pinned && !hasPinnedAncestor(row.session),
    ),
  };
}

export function deriveSidebarSessionKind(session: Session): SidebarSessionKind {
  if (
    session.context_type === "durable_agent" ||
    session.context_id?.startsWith("durable_agent:")
  ) {
    return "durable";
  }
  if (
    isBootProfileProvider(session.provider) ||
    isCLIProviderAlias(session.provider)
  ) {
    return "cli";
  }
  return "api";
}

export function deriveSidebarActivityState(
  session: Session,
  presence: SidebarPresenceMaps,
): SidebarActivitySummary {
  if (isArchivedStatus(session.status))
    return { state: "archived", label: "Archived" };
  if (session.halted_at) return { state: "halted", label: "Halted" };
  if (isStoppedStatus(session.status, session.runtime_state)) {
    return { state: "stopped", label: "Stopped" };
  }
  if (isFailureStatus(session.status, session.runtime_state)) {
    return { state: "failed", label: "Failed" };
  }
  if (presence.pendingTools.has(session.id)) {
    return { state: "pending_action", label: "Waiting on tool or user action" };
  }
  if (presence.activeStreams.has(session.id)) {
    return { state: "working", label: "Working now" };
  }
  if (
    presence.cliActiveSessions.has(session.id) ||
    isResidentRuntimeOnline(session.runtime_state)
  ) {
    return { state: "online", label: "Runtime online" };
  }
  return { state: "idle", label: "Idle" };
}

export function deriveSidebarSessionSummary(
  session: Session,
): SidebarSessionSummary {
  const kind = deriveSidebarSessionKind(session);
  if (kind === "durable") {
    return {
      kind,
      kindLabel: "Durable agent session",
      metadataLabel: durableMetadataLabel(session),
    };
  }
  if (kind === "cli") {
    return {
      kind,
      kindLabel: isBootProfileProvider(session.provider)
        ? "Boot-profile CLI session"
        : "CLI harness session",
      metadataLabel: cliMetadataLabel(session),
    };
  }
  return {
    kind,
    kindLabel: "API chat session",
    metadataLabel: compactProviderModelLabel(session.provider, session.model),
  };
}

export function compactProviderModelLabel(
  provider: string,
  model: string,
): string {
  const cleanProvider = humanizeProvider(provider);
  const cleanModel = humanizeModel(model);
  if (cleanProvider && cleanModel) return `${cleanProvider} / ${cleanModel}`;
  return cleanModel || cleanProvider || "API chat";
}

function durableMetadataLabel(session: Session): string {
  if (session.context_id)
    return `durable / ${compactIdentifier(session.context_id)}`;
  return compactProviderModelLabel(session.provider, session.model);
}

function cliMetadataLabel(session: Session): string {
  if (isBootProfileProvider(session.provider)) {
    return `boot / ${compactIdentifier(session.provider.replace(/^bootprofile:/, ""))}`;
  }
  const runtime = inferredRuntimeLabel(session.provider);
  const model = humanizeModel(session.model);
  return model ? `${runtime} / ${model}` : runtime;
}

function inferredRuntimeLabel(provider: string): string {
  if (provider === "pty-claude" || provider === "pty") return "stdio";
  if (
    provider === "codex" ||
    provider === "opencode" ||
    provider.startsWith("sub-")
  ) {
    return "subprocess";
  }
  if (provider === "pty-codex") return "subprocess";
  return "CLI";
}

function isBootProfileProvider(provider: string): boolean {
  return (
    provider.startsWith("bootprofile:") &&
    provider.length > "bootprofile:".length
  );
}

function isCLIProviderAlias(provider: string): boolean {
  return (
    CLI_PROVIDER_ALIASES.has(provider) ||
    provider.startsWith("pty-") ||
    provider.startsWith("sub-")
  );
}

function isArchivedStatus(status: string): boolean {
  return status === "archived";
}

function isStoppedStatus(
  status: string,
  runtimeState?: string | null,
): boolean {
  return status === "stopped" || status === "done" || runtimeState === "done";
}

function isFailureStatus(
  status: string,
  runtimeState?: string | null,
): boolean {
  return (
    status === "failed" ||
    status === "halted" ||
    status === "error" ||
    runtimeState === "failed" ||
    runtimeState === "orphaned"
  );
}

function isResidentRuntimeOnline(runtimeState?: string | null): boolean {
  return (
    runtimeState === "running" ||
    runtimeState === "launching" ||
    runtimeState === "starting"
  );
}

function humanizeProvider(provider: string): string {
  if (!provider) return "";
  if (provider === "openai") return "OpenAI";
  if (provider === "anthropic") return "Anthropic";
  return compactIdentifier(provider);
}

function humanizeModel(model: string): string {
  if (!model) return "";
  return compactIdentifier(model.replace(/^models\//, ""));
}

function compactIdentifier(value: string): string {
  return value
    .replace(/^bootprofile:/, "")
    .replace(/^pty-/, "")
    .replace(/_/g, "-");
}

function toTreeRow(
  session: Session,
  depth: number,
  byID: ReadonlyMap<string, Session>,
): SidebarSessionTreeRow {
  const parentSessionId = session.parent_session_id || null;
  return {
    session,
    depth: parentSessionId && byID.has(parentSessionId) ? depth : 0,
    parentSessionId,
    rootSessionId: session.root_session_id || null,
    relation: session.relation || null,
    missingParent: !!parentSessionId && !byID.has(parentSessionId),
  };
}
