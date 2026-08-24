/**
 * CW-20260517-0006 — interactive cards hydrate their persisted response.
 *
 * Envelope-audit Issue 2: respond to an approval/proposal/etc. card, reload
 * the page, and the card reset to 'pending' — the resolved state was lost
 * even though the backend persisted the response and `injectEnvelopePriorResponses`
 * injects a wrap-level `prior_response` into the envelope JSON.
 *
 * Backend persistence is sound; the FRONTEND did not consume `prior_response`.
 * Only `InterviewCard` hydrated. These tests pin that every other interactive
 * card now seeds its decision/answered state from `prior_response` so a
 * settled card renders its terminal (resolved) view on reload — never the
 * pending input affordances.
 */

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ApprovalCard } from "@/components/chat/envelopes/ApprovalCard";
import { ProposalCard } from "@/components/chat/envelopes/ProposalCard";
import { ConfirmationCard } from "@/components/chat/envelopes/primitives/ConfirmationCard";
import { ElicitationPromptCard } from "@/components/chat/envelopes/ElicitationPromptCard";
import type { Envelope } from "@/lib/types";
import type { ResponseV1 } from "@/lib/envelope-response";

afterEach(cleanup);

function envelope(type: string, data: Record<string, unknown>, prior?: ResponseV1): Envelope {
  return { kind: "agent_envelope", version: 1, type, id: `env-${type}`, data, prior_response: prior };
}

function response(over: Partial<ResponseV1>): ResponseV1 {
  return { v: 1, kind: "x", id: "env-x", status: "submitted", ...over };
}

describe("interactive cards hydrate from prior_response", () => {
  it("ApprovalCard renders the approved terminal state when prior_response.approved=true", () => {
    const env = envelope(
      "approval-card",
      { description: "Deploy to prod" },
      response({ status: "submitted", data: { approved: true } }),
    );
    const { container } = render(<ApprovalCard envelope={env} />);
    expect(container.textContent).toContain("Approved");
    expect(container.textContent).not.toContain("Approval required");
  });

  it("ApprovalCard renders the rejected terminal state when prior_response.approved=false", () => {
    const env = envelope(
      "approval-card",
      { description: "Deploy to prod" },
      response({ status: "submitted", data: { approved: false } }),
    );
    const { container } = render(<ApprovalCard envelope={env} />);
    expect(container.textContent).toContain("Rejected");
  });

  it("ApprovalCard stays pending with no prior_response", () => {
    const env = envelope("approval-card", { description: "Deploy to prod" });
    const { container } = render(<ApprovalCard envelope={env} />);
    expect(container.textContent).toContain("Approval required");
  });

  it("ProposalCard renders the applied state when prior_response is submitted", () => {
    const env = envelope(
      "proposal-card",
      { type: "rename", payload: { name: "x" } },
      response({ status: "submitted" }),
    );
    const { container } = render(<ProposalCard envelope={env} />);
    expect(container.textContent).toContain("Applied");
  });

  it("ProposalCard renders the dismissed state when prior_response is canceled", () => {
    const env = envelope(
      "proposal-card",
      { type: "rename", payload: { name: "x" } },
      response({ status: "canceled" }),
    );
    const { container } = render(<ProposalCard envelope={env} />);
    expect(container.textContent).toContain("Dismissed");
  });

  it("ConfirmationCard renders the confirmed state when prior_response is submitted", () => {
    const env = envelope(
      "confirmation-card",
      { title: "Drop table", message: "Sure?" },
      response({ status: "submitted", data: { confirmed: true } }),
    );
    const { container } = render(<ConfirmationCard envelope={env} />);
    expect(container.textContent).toContain("Confirmed");
    expect(container.textContent).not.toContain("Confirm action");
  });

  it("ConfirmationCard renders the canceled state when prior_response is canceled", () => {
    const env = envelope(
      "confirmation-card",
      { title: "Drop table", message: "Sure?" },
      response({ status: "canceled", data: { confirmed: false } }),
    );
    const { container } = render(<ConfirmationCard envelope={env} />);
    expect(container.textContent).toContain("Canceled");
  });

  it("ApprovalCard (subagent-spawn-approval flavor) renders the approved state when prior_response is submitted", () => {
    const env = envelope(
      "subagent-spawn-approval",
      { run_id: "run-1", role: "backend", prompt: "do x", mode: "async" },
      response({ status: "submitted" }),
    );
    const { container } = render(<ApprovalCard envelope={env} />);
    expect(container.textContent).toContain("approved");
  });

  it("ApprovalCard (subagent-spawn-approval flavor) renders the rejected state when prior_response is canceled", () => {
    const env = envelope(
      "subagent-spawn-approval",
      { run_id: "run-1", role: "backend", prompt: "do x", mode: "async" },
      response({ status: "canceled" }),
    );
    const { container } = render(<ApprovalCard envelope={env} />);
    expect(container.textContent).toContain("rejected");
  });

  it("ElicitationPromptCard renders the accepted state when prior_response.action=accept", () => {
    const env = envelope(
      "elicitation-prompt",
      { elicitation_id: "e-1", message: "Pick one", schema_type: "string", origin: "server", timeout_at: "" },
      response({ status: "submitted", data: { action: "accept", content: "blue" } }),
    );
    const { container } = render(<ElicitationPromptCard envelope={env} />);
    expect(container.textContent).toContain("Response submitted");
  });

  it("ElicitationPromptCard renders the declined state when prior_response.action=decline", () => {
    const env = envelope(
      "elicitation-prompt",
      { elicitation_id: "e-1", message: "Pick one", schema_type: "boolean", origin: "server", timeout_at: "" },
      response({ status: "submitted", data: { action: "decline" } }),
    );
    const { container } = render(<ElicitationPromptCard envelope={env} />);
    expect(container.textContent).toContain("Declined");
  });
});
