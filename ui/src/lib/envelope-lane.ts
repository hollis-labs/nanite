import type { Envelope } from "@/lib/types";

export type EnvelopeDisplayClass = "content" | "alert" | "action-required";

export function shouldRenderStandalonePluginEnvelope(envelope: Envelope): boolean {
  const displayClass = envelope.display_class;
  if (displayClass === "alert" || displayClass === "action-required") {
    return true;
  }
  if (displayClass === "content") {
    return false;
  }
  return !(envelope.render_target && !envelope.render_target_blocked);
}
