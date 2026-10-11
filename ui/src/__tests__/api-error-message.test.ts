import { describe, expect, it } from "vitest";
import { apiErrorMessage } from "@/lib/api";

const res = (status: number, body: string) => new Response(body, { status });

describe("apiErrorMessage", () => {
  it("carries the typed envelope message", async () => {
    const msg = await apiErrorMessage(
      res(422, JSON.stringify({ error: { message: "select a provider and model" } })),
      "Failed to create agent session",
    );
    expect(msg).toBe("Failed to create agent session: select a provider and model");
  });
  it("carries a plain error string", async () => {
    expect(await apiErrorMessage(res(500, JSON.stringify({ error: "boom" })), "Failed")).toBe(
      "Failed: boom",
    );
  });
  it("falls back to the status for a non-JSON body", async () => {
    expect(await apiErrorMessage(res(502, "bad gateway"), "Failed")).toBe("Failed: 502");
  });
});
