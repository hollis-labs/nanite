import { z } from "zod";

export const RESPONSE_V1_VERSION = 1;

export const ResponseStatus = {
  Submitted: "submitted",
  Canceled: "canceled",
  Partial: "partial",
} as const;

export const AnswerSchema = z.object({
  questionId: z.string(),
  value: z.unknown(),
  acceptedSuggestion: z.boolean().optional(),
  note: z.string().optional(),
});

export const DecisionSchema = z.object({
  itemId: z.string(),
  action: z.string(),
  note: z.string().optional(),
  meta: z.record(z.string(), z.unknown()).optional(),
});

export const ResponseV1Schema = z.object({
  v: z.literal(RESPONSE_V1_VERSION),
  kind: z.string().min(1),
  id: z.string().min(1),
  status: z.enum([ResponseStatus.Submitted, ResponseStatus.Canceled, ResponseStatus.Partial]),
  data: z.record(z.string(), z.unknown()).optional(),
  answers: z.array(AnswerSchema).optional(),
  decisions: z.array(DecisionSchema).optional(),
  session_id: z.string().optional(),
});

export type Answer = z.infer<typeof AnswerSchema>;
export type Decision = z.infer<typeof DecisionSchema>;
export type ResponseV1 = z.infer<typeof ResponseV1Schema>;
export type ResponseStatusT = ResponseV1["status"];

export interface EnvelopeResponseResult {
  ok: boolean;
  message_id?: string;
  follow_up?: string;
}

export interface PriorResponseConflict {
  response_status: ResponseStatusT;
  response: ResponseV1;
}

export class EnvelopeResponseError extends Error {
  status: number;
  prior?: PriorResponseConflict;
  constructor(message: string, status: number, prior?: PriorResponseConflict) {
    super(message);
    this.name = "EnvelopeResponseError";
    this.status = status;
    if (prior) this.prior = prior;
  }
}

/**
 * Build a validated ResponseV1 without emitting it. Throws if the shape is
 * invalid — callers in cards should already be passing well-formed payloads,
 * so this is a belt-and-suspenders check before network.
 */
export function buildResponseV1(input: ResponseV1): ResponseV1 {
  return ResponseV1Schema.parse(input);
}

/**
 * POST /api/envelopes/{id}/respond. Returns the backend ack or throws
 * EnvelopeResponseError on non-2xx. On 409, `error.prior` carries the
 * previously-recorded response so the caller can reflect that state.
 */
export async function submitEnvelopeResponse(
  envelopeId: string,
  payload: ResponseV1,
): Promise<EnvelopeResponseResult> {
  const validated = buildResponseV1(payload);
  const res = await fetch(`/api/envelopes/${encodeURIComponent(envelopeId)}/respond`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(validated),
  });
  if (res.status === 409) {
    const body = (await res.json().catch(() => ({}))) as Partial<PriorResponseConflict>;
    const prior =
      body.response_status && body.response
        ? { response_status: body.response_status, response: body.response }
        : undefined;
    throw new EnvelopeResponseError("envelope already responded", 409, prior);
  }
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new EnvelopeResponseError(
      body.error || `envelope response failed: ${res.status}`,
      res.status,
    );
  }
  return (await res.json()) as EnvelopeResponseResult;
}

// --- Dev smoke check -------------------------------------------------------
// Stopgap until vitest lands (BLG-20260415-004 / BLG-20260415-005). Runs once
// on module init in dev so an accidental schema drift surfaces immediately
// instead of at first user interaction.
if (import.meta.env?.DEV) {
  const fixtures: ResponseV1[] = [
    { v: 1, kind: "approval", id: "env-01", status: "submitted", data: { approved: true } },
    { v: 1, kind: "approval", id: "env-02", status: "canceled" },
    {
      v: 1,
      kind: "collect_feedback",
      id: "env-03",
      status: "submitted",
      answers: [{ questionId: "q-0", value: "ok" }],
    },
    {
      v: 1,
      kind: "triage_items",
      id: "env-04",
      status: "partial",
      decisions: [{ itemId: "i-0", action: "defer" }],
    },
  ];
  for (const fx of fixtures) {
    try {
      ResponseV1Schema.parse(fx);
    } catch (err) {
      console.error("[envelope-response] smoke-check failed for fixture", fx, err);
    }
  }
  try {
    ResponseV1Schema.parse({ v: 2, kind: "x", id: "y", status: "submitted" });
    console.error("[envelope-response] smoke-check: v=2 should have been rejected");
  } catch {
    // expected
  }
}
