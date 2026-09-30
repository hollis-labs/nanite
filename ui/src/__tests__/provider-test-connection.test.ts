import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";

// CW-20260930-0101: an unregistered route used to come back as the SPA's
// 200 text/html, and a 404 was mapped to success. Neither may read as ok.
function stubFetch(response: Partial<Response> & { json: () => Promise<unknown> }) {
  const mock = vi.fn(async () => response as Response);
  vi.stubGlobal("fetch", mock);
  return mock;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("api.testProviderConnection", () => {
  it("POSTs to the provider's test route and returns the check result", async () => {
    const mock = stubFetch({
      ok: true,
      status: 200,
      json: async () => ({ message: "Key accepted by Anthropic", ok: true, status: "accepted" }),
    });
    const result = await api.testProviderConnection("anthropic-001");
    expect(result).toEqual({ message: "Key accepted by Anthropic", ok: true, status: "accepted" });
    const [url, init] = mock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toMatch(/\/providers\/anthropic-001\/test$/);
    expect(init.method).toBe("POST");
  });

  it("returns a failed check as a result, not an exception", async () => {
    stubFetch({
      ok: true,
      status: 200,
      json: async () => ({ message: "Anthropic rejected the key", ok: false, status: "rejected" }),
    });
    const result = await api.testProviderConnection("anthropic-001");
    expect(result.ok).toBe(false);
    expect(result.status).toBe("rejected");
  });

  it("throws on a 404 instead of treating it as success", async () => {
    stubFetch({ ok: false, status: 404, json: async () => ({ error: "provider not found" }) });
    await expect(api.testProviderConnection("nope")).rejects.toThrow("provider not found");
  });

  it("throws on a 200 whose body is not JSON (the SPA fallback)", async () => {
    stubFetch({
      ok: true,
      status: 200,
      json: async () => {
        throw new SyntaxError("Unexpected token '<'");
      },
    });
    await expect(api.testProviderConnection("anthropic-001")).rejects.toThrow(
      "Connection test failed: 200",
    );
  });

  it("throws on a 200 JSON body that is not a check result", async () => {
    stubFetch({ ok: true, status: 200, json: async () => ({ something: "else" }) });
    await expect(api.testProviderConnection("anthropic-001")).rejects.toThrow(
      "Connection test failed: 200",
    );
  });
});
