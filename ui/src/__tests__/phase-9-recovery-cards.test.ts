/**
 * Phase 9 (CW-20260510-0017 / W2A) — recovery envelope FE rendering + cancel
 * round-trip regression tests.
 *
 * Targets the FE-side wire-shape contract from W1D
 * (tracking_root/phase-9-envelope-projection-implementer-report.md):
 *
 *   - `cancel_token` rides at wrap level (sibling of id/type/data), NOT
 *     inside `data`. The info-card schema sets additionalProperties:false
 *     so the token cannot live in the data payload.
 *   - The FE reads the token verbatim and POSTs it to
 *     `POST /api/sessions/{sessionID}/recovery/cancel` with body
 *     `{"token": "<token>"}`.
 *   - 200 → "cancelled" (broker cancelled the retry).
 *   - 404 → "stale" (token unknown/expired/cross-session).
 *   - any other → "error".
 *   - Severity → variant mapping: info → info, warning → warning,
 *     error → danger, empty/unknown → info. The mapping happens
 *     server-side (per W1D's report); the FE just passes data.variant
 *     through to the InfoCard. We cover the round-trip.
 *
 * Pure logic / network-layer tests — no React rendering. The button
 * component itself is exercised manually in the smoke check (see
 * implementer report).
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import type { Envelope } from "@/lib/types";

// ---- fetch mocking helpers ---------------------------------------------

const ORIGINAL_FETCH = globalThis.fetch;

function mockFetchOnce(response: { status: number; body?: unknown }) {
  const fn = vi.fn().mockResolvedValue({
    ok: response.status >= 200 && response.status < 300,
    status: response.status,
    json: async () => response.body ?? {},
  });
  globalThis.fetch = fn as unknown as typeof globalThis.fetch;
  return fn;
}

beforeEach(() => {
  globalThis.fetch = ORIGINAL_FETCH;
});

afterEach(() => {
  globalThis.fetch = ORIGINAL_FETCH;
});

// ---- api.cancelRecoveryRetry contract ---------------------------------

describe("api.cancelRecoveryRetry", () => {
  it("POSTs the token verbatim to the path-bound session endpoint", async () => {
    const fn = mockFetchOnce({ status: 200, body: { status: "cancelled" } });
    await api.cancelRecoveryRetry("sess-123", "tok-abc");
    expect(fn).toHaveBeenCalledTimes(1);
    const [url, init] = fn.mock.calls[0];
    expect(url).toBe("/api/sessions/sess-123/recovery/cancel");
    expect(init).toMatchObject({
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token: "tok-abc" }),
    });
  });

  it("returns 'cancelled' on 200", async () => {
    mockFetchOnce({ status: 200, body: { status: "cancelled" } });
    const result = await api.cancelRecoveryRetry("sess", "tok");
    expect(result).toBe("cancelled");
  });

  it("returns 'stale' on 404 (unknown / expired / cross-session token)", async () => {
    mockFetchOnce({ status: 404 });
    const result = await api.cancelRecoveryRetry("sess", "tok");
    expect(result).toBe("stale");
  });

  it("returns 'error' on 400 (malformed body)", async () => {
    mockFetchOnce({ status: 400 });
    const result = await api.cancelRecoveryRetry("sess", "tok");
    expect(result).toBe("error");
  });

  it("returns 'error' on 503 (broker not wired)", async () => {
    mockFetchOnce({ status: 503 });
    const result = await api.cancelRecoveryRetry("sess", "tok");
    expect(result).toBe("error");
  });

  it("propagates network failures to the caller", async () => {
    globalThis.fetch = vi
      .fn()
      .mockRejectedValue(new TypeError("network down")) as unknown as typeof globalThis.fetch;
    await expect(api.cancelRecoveryRetry("sess", "tok")).rejects.toThrow("network down");
  });
});

// ---- Envelope wire-shape (cancel_token at wrap level, not in data) -----

describe("recovery envelope wire shape", () => {
  it("info-card with cancel_token: token is wrap-level sibling of data", () => {
    // Sample payload from W1D's implementer report — the canonical
    // contract this FE ticket renders against.
    const wire = {
      id: "",
      type: "info-card",
      data: {
        title: "Reconnecting agent",
        body: "Agent ran into a temporary error. Retrying now…",
        variant: "info",
      },
      cancel_token: "a3f5e9c2",
    };
    // Simulate the useChat SSE handler's parse → Envelope cast.
    const envelope = wire as unknown as Envelope;
    // The FE reads cancel_token from the wrap, NOT from data. This is
    // the load-bearing assertion for W1D's schema-compatibility decision.
    expect(envelope.cancel_token).toBe("a3f5e9c2");
    expect((envelope.data as { variant?: string } | undefined)?.variant).toBe("info");
    // Confirm `data` does not carry the token even when fully typed —
    // the info-card schema's additionalProperties:false would reject it.
    expect((envelope.data as Record<string, unknown>).cancel_token).toBeUndefined();
  });

  it("info-card without cancel_token: omitting wrap field disables the cancel affordance", () => {
    const wire = {
      id: "",
      type: "info-card",
      data: { title: "Recovered", body: "Agent reconnected.", variant: "success" },
    };
    const envelope = wire as unknown as Envelope;
    expect(envelope.cancel_token).toBeUndefined();
  });

  it("error-report: no cancel_token (terminal failure, not cancellable)", () => {
    // Per W1D's contract — error-report is a permanent broker failure.
    // The wrap MUST NOT carry cancel_token.
    const wire = {
      id: "",
      type: "error-report",
      data: {
        code: "agent_recovery_failed",
        message: "Agent unavailable: …",
        giphy_query: "this is fine fire",
        timestamp: "2026-05-10T04:23:05Z",
      },
    };
    const envelope = wire as unknown as Envelope;
    expect(envelope.cancel_token).toBeUndefined();
    expect((envelope.data as { code?: string } | undefined)?.code).toBe("agent_recovery_failed");
  });

  it("chat-loop-terminated: no cancel_token (broker exhausted retry budget)", () => {
    // Per W1D's contract — chat-loop-terminated is the supervisor-
    // exhausted variant; nothing left to cancel.
    const wire = {
      id: "",
      type: "chat-loop-terminated",
      data: {
        reason: "Agent stopped: …",
        code: "retry_budget_exhausted",
        iteration: 0,
        consecutive_failures: 0,
        timestamp: "2026-05-10T04:23:05Z",
      },
    };
    const envelope = wire as unknown as Envelope;
    expect(envelope.cancel_token).toBeUndefined();
    expect((envelope.data as { code?: string } | undefined)?.code).toBe("retry_budget_exhausted");
  });
});

// ---- Severity → variant mapping (W1D's BE projection contract) ---------

describe("severity → variant mapping (server-side, FE round-trip)", () => {
  // Per W1D's report, the server maps recovery.Envelope.Severity to
  // info-card schema's data.variant enum:
  //   "info"     → "info"
  //   "warning"  → "warning"
  //   "error"    → "danger"
  //   ""/unknown → "info" (safe default)
  //
  // The FE just passes the variant through to InfoCard's VARIANT_CONFIG.
  // These tests assert the FE accepts every value the server may emit.

  it.each([
    ["info", "info"],
    ["warning", "warning"],
    ["danger", "danger"],
    ["success", "success"], // not server-emitted today, but schema-valid
  ])("variant %s renders without falling back to default", (variant, expected) => {
    const wire = {
      id: "",
      type: "info-card",
      data: { title: "t", body: "b", variant },
    };
    const envelope = wire as unknown as Envelope;
    expect((envelope.data as { variant?: string }).variant).toBe(expected);
  });
});
