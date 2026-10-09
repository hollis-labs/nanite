import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AdapterBadge } from "@/components/chat/AdapterBadge";

afterEach(cleanup);

describe("headless adapter labels", () => {
  it.each([
    "pty",
    "pty-claude",
    "sub-codex",
    "claude",
    "codex",
    "copilot",
    "pi",
  ])("labels %s as CLI", (provider) => {
    render(<AdapterBadge provider={provider} />);
    expect(screen.getByText("CLI").getAttribute("title")).toBe("CLI session");
    expect(screen.queryByText("PTY")).toBeNull();
  });
  it("preserves API labels", () => {
    render(<AdapterBadge provider="anthropic" />);
    expect(screen.getByText("API").getAttribute("title")).toBe("API session");
  });
});
