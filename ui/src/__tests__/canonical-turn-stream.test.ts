import { afterEach, describe, expect, it, vi } from "vitest";
import { CanonicalTurnStream } from "@/lib/canonicalTurnStream";

const event = (seq: number, verb: string, extra: Record<string, unknown> = {}) =>
  `event: ${verb}\nid: ${seq}\ndata: ${JSON.stringify({ v: "1", seq, run_id: "turn", verb, ...extra })}\n\n`;
const response = (body: string) =>
  new Response(body, {
    headers: { "Content-Type": "text/event-stream", "X-Chat-Encoding": "chatstream/v1" },
  });
const collect = (stream: CanonicalTurnStream) => {
  const data: Array<{ type: string; data: Record<string, unknown> }> = [];
  const done = new Promise<void>((resolve, reject) => {
    for (const type of ["delta", "replace_content", "approval_request", "stream_end", "error"]) {
      stream.addEventListener(type, (e) => {
        data.push({ type, data: JSON.parse((e as MessageEvent).data) });
        if (type === "stream_end" || type === "error") resolve();
      });
    }
    stream.onerror = () => reject(new Error("transport recovery exhausted"));
  });
  return { data, done };
};
afterEach(() => vi.unstubAllGlobals());
describe("canonical native browser stream", () => {
  it("renders phased text and replacement with one terminal", async () => {
    const wire =
      event(1, "run.start") +
      event(2, "message.start") +
      event(3, "part.start", { part_id: "p", kind: "text", meta: { phase: "final" } }) +
      event(4, "part.delta", { part_id: "p", text: "draft" }) +
      event(4, "part.delta", { part_id: "p", text: "draft" }) +
      event(5, "activity", { kind: "chatstream.replace_content", value: { content: "answer" } }) +
      event(6, "part.end", { part_id: "p" }) +
      event(7, "message.end") +
      event(8, "usage", { usage: { scope: "cumulative", uncached_input: 5, output: 2 } }) +
      event(9, "run.finish");
    const fetchMock = vi.fn().mockResolvedValue(response(wire));
    vi.stubGlobal("fetch", fetchMock);
    const observed = collect(new CanonicalTurnStream("view/1", "turn"));
    await observed.done;
    expect(observed.data.map((e) => e.type)).toEqual(["delta", "replace_content", "stream_end"]);
    expect(observed.data[0]?.data).toMatchObject({ content: "draft", phase: "final", event_id: 4 });
    expect(observed.data[1]?.data.content).toBe("answer");
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/agent/v1/sessions/view%2F1/turns/turn/events");
  });
  it("recovers a retention gap from authoritative status", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(response(event(0, "gap", { reason: "retention", from: 1, to: 90 })))
      .mockResolvedValueOnce(
        Response.json({
          state: "completed",
          content: "persisted answer",
          event_checkpoint: 100,
          message: { run_id: "turn", status: "finished" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);
    const observed = collect(new CanonicalTurnStream("view", "turn"));
    await observed.done;
    expect(observed.data.map((e) => e.type)).toEqual(["replace_content", "stream_end"]);
    expect(observed.data[0]?.data.content).toBe("persisted answer");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
  it("resumes with Last-Event-ID and restores only the pending once prompt", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({
          state: "input_required",
          content: "partial",
          event_checkpoint: 10,
          pending_approval_id: "pending",
          message: {
            run_id: "turn",
            parts: [],
            approvals: [
              { id: "answered", call_id: "old", descriptor: { tool: "old" } },
              {
                id: "pending",
                call_id: "call",
                descriptor: { tool: "write", input: {} },
                expires_at: "2099-01-01T00:00:00Z",
              },
            ],
          },
        }),
      )
      .mockResolvedValueOnce(response(event(11, "run.abort", { reason: "canceled" })));
    vi.stubGlobal("fetch", fetchMock);
    const observed = collect(new CanonicalTurnStream("view", "turn", 2));
    await observed.done;
    expect(fetchMock.mock.calls[1]?.[1].headers).toEqual({ "Last-Event-ID": "10" });
    const prompts = observed.data.filter((e) => e.type === "approval_request");
    expect(prompts).toHaveLength(1);
    expect(JSON.parse(prompts[0]?.data.data as string)).toMatchObject({
      request_id: "pending",
      run_id: "turn",
      call_id: "call",
      supported_scopes: ["once"],
    });
  });
  it("uses failed status after EOF instead of synthesizing success", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(response(event(1, "run.start")))
      .mockResolvedValueOnce(
        Response.json({
          state: "failed",
          event_checkpoint: 2,
          content: "",
          message: {
            run_id: "turn",
            status: "errored",
            error: { code: "process_lost", message: "native process ended" },
          },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);
    const observed = collect(new CanonicalTurnStream("view", "turn"));
    await observed.done;
    expect(observed.data.map((e) => e.type)).toEqual(["replace_content", "error"]);
    expect(observed.data[1]?.data.error).toBe("native process ended");
  });
  it("rejects a different run without rendering its terminal outcome", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValueOnce(response(event(1, "run.finish", { run_id: "other" }))),
    );
    const observed = collect(new CanonicalTurnStream("view", "turn"));
    await expect(observed.done).rejects.toThrow("transport recovery exhausted");
    expect(observed.data).toEqual([]);
  });
});
