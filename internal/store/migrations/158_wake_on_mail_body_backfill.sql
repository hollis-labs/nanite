-- +goose Up
-- 158_wake_on_mail_body_backfill.sql
-- CW-20260911-0075.
--
-- Repoints both seeded wake_on_mail reflexes at Nanite's own mailbox tools and
-- makes the discharge explicit. Two independent defects in the shipped bodies:
--
--   1. They named `mux_message_inbox`, which reads a DIFFERENT product's
--      mailbox. The trigger reads Nanite's agent_messages
--      (reflexes/state.go mailUnreadCount), so the prescribed action could not
--      affect the condition that fired it. Where mux tools are on the surface
--      it was also destructive: that tool marks returned messages delivered,
--      so a partial triage silently dropped the remainder from every later
--      inbox call.
--
--   2. Naming only the read cannot discharge the reflex at all. The trigger
--      counts rows with status = 'unread'; `message_inbox` does not mark,
--      which is exactly what makes it safe to call repeatedly and also what
--      means reading never decrements the count. `message_ack` is the
--      discharge. A body without it re-fires every tick forever, whichever
--      mailbox it points at.
--
-- Why this is a migration and not only a seeds.go edit: SeedBaseReflexes
-- (internal/agent/reflexes/seeds.go) skips any reflex that already exists
-- (CountClassBaseReflexByName > 0 -> continue), so the source change reaches
-- fresh databases only. Every already-seeded install keeps the old body
-- indefinitely without this.
--
-- Per AGENTS.md this carries no VALUES clause for application-owned rows --
-- seed.go owns creation, and an UPDATE backfill is the allowed idiom. Each
-- statement is scoped to created_by = 'system' AND the exact old body, so an
-- operator's own edit to a wake_on_mail body is left alone and a re-run is a
-- no-op.

UPDATE agent_reflexes
   SET action_spec = json_set(
         action_spec,
         '$.body',
         'Mail in the inbox — call message_inbox to read it, then message_ack each message you handled before continuing. Reading does not clear the unread count; the ack is what stops this reminder.'
       )
 WHERE name        = 'wake_on_mail'
   AND class_tag   = 'process'
   AND created_by  = 'system'
   AND action_kind = 'inject_reminder'
   AND json_extract(action_spec, '$.body') =
       'Mail in the inbox — call mux_message_inbox to triage before continuing.';

UPDATE agent_reflexes
   SET action_spec = json_set(
         action_spec,
         '$.body',
         'Mail in the inbox — call message_inbox to read it, then message_ack each message you handled. Reading does not clear the unread count; the ack is what stops this reminder.'
       )
 WHERE name        = 'wake_on_mail'
   AND class_tag   = 'advisor'
   AND created_by  = 'system'
   AND action_kind = 'inject_reminder'
   AND json_extract(action_spec, '$.body') =
       'Mail in the inbox — call mux_message_inbox to triage.';

-- +goose Down
-- Restores the original bodies, mux prefix and missing discharge included, so
-- a downgrade leaves the rows exactly as the pre-158 seed wrote them. Same
-- scoping as the Up direction: system-seeded rows carrying the corrected body
-- and nothing else.

UPDATE agent_reflexes
   SET action_spec = json_set(
         action_spec,
         '$.body',
         'Mail in the inbox — call mux_message_inbox to triage before continuing.'
       )
 WHERE name        = 'wake_on_mail'
   AND class_tag   = 'process'
   AND created_by  = 'system'
   AND action_kind = 'inject_reminder'
   AND json_extract(action_spec, '$.body') =
       'Mail in the inbox — call message_inbox to read it, then message_ack each message you handled before continuing. Reading does not clear the unread count; the ack is what stops this reminder.';

UPDATE agent_reflexes
   SET action_spec = json_set(
         action_spec,
         '$.body',
         'Mail in the inbox — call mux_message_inbox to triage.'
       )
 WHERE name        = 'wake_on_mail'
   AND class_tag   = 'advisor'
   AND created_by  = 'system'
   AND action_kind = 'inject_reminder'
   AND json_extract(action_spec, '$.body') =
       'Mail in the inbox — call message_inbox to read it, then message_ack each message you handled. Reading does not clear the unread count; the ack is what stops this reminder.';
