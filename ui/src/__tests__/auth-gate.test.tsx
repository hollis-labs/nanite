import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AuthGate } from "@/components/AuthGate";

function json(status: number, body: unknown = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("AuthGate", () => {
  const realFetch = window.fetch;
  beforeEach(() => {
    vi.restoreAllMocks();
  });
  afterEach(() => {
    window.fetch = realFetch;
  });

  it("shows the login screen when there is no session and does not render the app", async () => {
    window.fetch = vi.fn(async () =>
      json(401, { error: { code: "unauthenticated" } }),
    ) as typeof fetch;
    render(
      <AuthGate>
        <div>the app</div>
      </AuthGate>,
    );
    expect(await screen.findByText("Sign in to Nanite")).toBeTruthy();
    expect(screen.queryByText("the app")).toBeNull();
  });

  it("signs in with a pasted credential, then renders the app", async () => {
    const calls: string[] = [];
    window.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : (input as Request).url;
      calls.push(`${init?.method ?? "GET"} ${url}`);
      if (url === "/api/auth/session") return json(401);
      if (url === "/api/auth/login") return json(200, { csrf_token: "csrf-1" });
      return json(200);
    }) as typeof fetch;
    render(
      <AuthGate>
        <div>the app</div>
      </AuthGate>,
    );
    const input = await screen.findByLabelText("Operator credential");
    fireEvent.change(input, { target: { value: "nan_operator_x" } });
    fireEvent.click(screen.getByText("Sign in"));
    expect(await screen.findByText("the app")).toBeTruthy();
    expect(calls).toContain("POST /api/auth/login");
  });

  it("shows an error for a rejected credential and stays signed out", async () => {
    window.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : (input as Request).url;
      return url === "/api/auth/login" ? json(401) : json(401);
    }) as typeof fetch;
    render(
      <AuthGate>
        <div>the app</div>
      </AuthGate>,
    );
    fireEvent.change(await screen.findByLabelText("Operator credential"), {
      target: { value: "nope" },
    });
    fireEvent.click(screen.getByText("Sign in"));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("not accepted"));
    expect(screen.queryByText("the app")).toBeNull();
  });
});
