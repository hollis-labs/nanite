-- S7 T4: MessageKind typed payloads.
--
-- Introduces two new columns on the messaging table:
--   - `kind` names the wire type of the message: request, reply,
--     notification (default), or handoff. Distinct from the existing
--     `type` column (which names the semantic role — message,
--     help_request, etc.) and from S5 envelope content types (UI
--     shapes).
--   - `payload_json` carries the envelope-type-specific payload per
--     S5's ResponseV1 shape. Existing rows default to '{}' so the
--     read path never has to null-check.
--
-- Vocabulary summary (documented in docs/messaging.md §Vocabulary):
--   Channel      — transport bucket (chat / inbox / alert)
--   Kind         — wire type (request / reply / notification / handoff)
--   EnvelopeType — S5 UI content shape (propose_object / collect_data)

ALTER TABLE a2a_messages ADD COLUMN kind TEXT NOT NULL DEFAULT 'notification'
  CHECK(kind IN ('request','reply','notification','handoff'));

ALTER TABLE a2a_messages ADD COLUMN payload_json TEXT NOT NULL DEFAULT '{}';
