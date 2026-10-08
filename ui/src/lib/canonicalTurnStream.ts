// Browser presentation adapter for the canonical per-run wire. Transport EOF,
// gaps and reconnects consult the authoritative snapshot; they create no run.
type Part = { id: string; kind: string; meta?: Record<string, unknown>; final?: unknown };
type Snapshot = {
  event_checkpoint: number;
  state: string;
  content: string;
  pending_approval_id?: string;
  message: {
    parts?: Part[];
    approvals?: Array<{
      id: string;
      call_id?: string;
      descriptor?: Record<string, unknown>;
      reason?: string;
      expires_at?: string;
    }>;
    error?: { code: string; message: string };
    run_id: string;
  };
};
type CanonicalEvent = {
  v: string;
  seq: number;
  run_id: string;
  verb: string;
  part_id?: string;
  kind?: string;
  meta?: Record<string, unknown>;
  text?: string;
  final?: Record<string, unknown>;
  value?: Record<string, unknown>;
  approval_id?: string;
  call_id?: string;
  descriptor?: Record<string, unknown>;
  reason?: string;
  expires_at?: string;
  message?: string;
  code?: string;
};

export class CanonicalTurnStream extends EventTarget {
  onerror: (() => void) | null = null;
  private controller = new AbortController();
  private closed = false;
  private cursor: number;
  private parts = new Map<string, Part>();
  private statusPath: string;
  private runId: string;
  constructor(viewId: string, turnId: string, after = 0) {
    super();
    this.cursor = after;
    this.runId = turnId;
    this.statusPath = `/api/agent/v1/sessions/${encodeURIComponent(viewId)}/turns/${encodeURIComponent(turnId)}`;
    queueMicrotask(() => {
      void this.run();
    });
  }
  close() {
    this.closed = true;
    this.controller.abort();
  }
  private emit(type: string, data: Record<string, unknown>, seq = 0) {
    if (!this.closed)
      this.dispatchEvent(
        new MessageEvent(type, { data: JSON.stringify({ ...data, event_id: seq }) }),
      );
  }
  private async snapshot(): Promise<boolean> {
    const response = await fetch(this.statusPath, {
      signal: this.controller.signal,
      redirect: "error",
      cache: "no-store",
    });
    if (!response.ok) throw new Error(`Turn status unavailable: ${response.status}`);
    const snapshot = (await response.json()) as Snapshot;
    if (
      !Number.isSafeInteger(snapshot.event_checkpoint) ||
      snapshot.event_checkpoint < 0 ||
      snapshot.message.run_id !== this.runId
    )
      throw new Error("Invalid turn checkpoint");
    this.cursor = snapshot.event_checkpoint;
    this.parts.clear();
    for (const part of snapshot.message.parts ?? []) this.parts.set(part.id, part);
    this.emit("replace_content", { content: snapshot.content });
    if (snapshot.state === "completed" || snapshot.state === "canceled") {
      this.emit("stream_end", { interrupted: snapshot.state === "canceled" });
      this.close();
      return true;
    }
    if (snapshot.state === "failed") {
      this.emit("error", {
        error: snapshot.message.error?.message ?? "Turn failed",
        structured_error: snapshot.message.error,
      });
      this.close();
      return true;
    }
    if (snapshot.state === "input_required") {
      for (const approval of snapshot.message.approvals ?? []) {
        if (
          approval.id !== snapshot.pending_approval_id ||
          (approval.expires_at && Date.parse(approval.expires_at) <= Date.now())
        )
          continue;
        this.emit("approval_request", {
          data: JSON.stringify({
            ...approval.descriptor,
            request_id: approval.id,
            run_id: snapshot.message.run_id,
            call_id: approval.call_id,
            reason: approval.reason,
            expires_at: approval.expires_at,
            supported_scopes: ["once"],
          }),
        });
      }
    }
    return false;
  }
  private apply(event: CanonicalEvent, name: string, id: string): boolean {
    if (
      event.v !== "1" ||
      event.verb !== name ||
      event.run_id !== this.runId ||
      !Number.isSafeInteger(event.seq) ||
      event.seq < 0
    )
      throw new Error("Invalid canonical event");
    if (
      ![
        "run.start",
        "usage",
        "run.finish",
        "run.error",
        "run.abort",
        "step.start",
        "step.finish",
        "message.start",
        "message.end",
        "part.start",
        "part.delta",
        "part.end",
        "approval.request",
        "activity",
        "raw",
        "gap",
      ].includes(event.verb)
    )
      throw new Error("Unknown canonical verb");
    if (event.verb === "gap") return false;
    if (id !== String(event.seq) || event.seq === 0)
      throw new Error("Invalid canonical checkpoint");
    if (event.seq <= this.cursor) return true;
    if (event.seq !== this.cursor + 1) return false;
    this.cursor = event.seq;
    const seq = event.seq;
    switch (event.verb) {
      case "part.start": {
        if (!event.part_id || !event.kind) throw new Error("Invalid canonical part");
        this.parts.set(event.part_id, { id: event.part_id, kind: event.kind, meta: event.meta });
        if (event.kind === "tool_call")
          this.emit(
            "tool_call",
            { tool: event.meta?.name, tool_id: event.meta?.call_id, detail: event.meta?.detail },
            seq,
          );
        break;
      }
      case "part.delta": {
        const part = event.part_id ? this.parts.get(event.part_id) : undefined;
        if (!part) throw new Error("Canonical delta has no part");
        if (part.kind === "text" || part.kind === "reasoning")
          this.emit(
            "delta",
            {
              content: event.text,
              phase: part.meta?.phase ?? (part.kind === "reasoning" ? "thinking" : "final"),
            },
            seq,
          );
        break;
      }
      case "part.end": {
        const part = event.part_id ? this.parts.get(event.part_id) : undefined;
        if (part?.kind === "tool_result")
          this.emit(
            "tool_result",
            {
              tool: part.meta?.name,
              tool_id: part.meta?.call_id,
              summary: event.final?.summary,
              is_error: part.meta?.is_error,
            },
            seq,
          );
        break;
      }
      case "approval.request":
        this.emit(
          "approval_request",
          {
            data: JSON.stringify({
              ...event.descriptor,
              request_id: event.approval_id,
              run_id: event.run_id,
              call_id: event.call_id,
              reason: event.reason,
              expires_at: event.expires_at,
              supported_scopes: ["once"],
            }),
          },
          seq,
        );
        break;
      case "activity":
        if (event.kind === "chatstream.replace_content")
          this.emit("replace_content", { content: event.value?.content }, seq);
        else if (event.kind?.startsWith("nanite.") && event.kind !== "nanite.turn_state")
          this.emit(event.kind.slice(7), event.value ?? {}, seq);
        break;
      case "run.finish":
      case "run.abort":
        this.emit("stream_end", {}, seq);
        this.close();
        break;
      case "run.error":
        this.emit(
          "error",
          { error: event.message, structured_error: { code: event.code, message: event.message } },
          seq,
        );
        this.close();
        break;
    }
    return true;
  }
  private async connection(): Promise<void> {
    const response = await fetch(`${this.statusPath}/events`, {
      headers: this.cursor ? { "Last-Event-ID": String(this.cursor) } : {},
      signal: this.controller.signal,
      redirect: "error",
      cache: "no-store",
    });
    if (!response.ok || !response.body)
      throw new Error(`Turn events unavailable: ${response.status}`);
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffered = "",
      data: string[] = [],
      name = "",
      id = "";
    try {
      while (!this.closed) {
        const chunk = await reader.read();
        if (chunk.done) return;
        buffered += decoder.decode(chunk.value, { stream: true });
        if (buffered.length > 16 * 1024 * 1024) throw new Error("Canonical frame too large");
        while (buffered.includes("\n")) {
          const boundary = buffered.indexOf("\n");
          const line = buffered.slice(0, boundary).replace(/\r$/, "");
          buffered = buffered.slice(boundary + 1);
          if (line === "") {
            if (data.length && !this.apply(JSON.parse(data.join("\n")) as CanonicalEvent, name, id))
              return;
            data = [];
            name = "";
            id = "";
          } else if (!line.startsWith(":")) {
            const colon = line.indexOf(":");
            const field = colon < 0 ? line : line.slice(0, colon);
            const value = colon < 0 ? "" : line.slice(colon + 1).replace(/^ /, "");
            if (field === "data") data.push(value);
            else if (field === "event") name = value;
            else if (field === "id") id = value;
          }
          if (data.reduce((size, item) => size + item.length, 0) > 16 * 1024 * 1024)
            throw new Error("Canonical frame too large");
          if (this.closed) return;
        }
      }
    } finally {
      await reader.cancel().catch(() => {});
      reader.releaseLock();
    }
  }
  private async run() {
    try {
      if (this.cursor && (await this.snapshot())) return;
      for (let attempt = 0; !this.closed && attempt < 4; attempt++) {
        try {
          await this.connection();
        } catch (error) {
          if (this.closed) return;
          if (attempt === 3) throw error;
        }
        if (this.closed || (await this.snapshot())) return;
        await new Promise((resolve) => setTimeout(resolve, Math.min(250 * 2 ** attempt, 2000)));
      }
      if (!this.closed) this.onerror?.();
    } catch {
      if (!this.closed) this.onerror?.();
    }
  }
}
