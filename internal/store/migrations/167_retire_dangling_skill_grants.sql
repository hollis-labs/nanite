-- +goose Up
-- 167_retire_dangling_skill_grants.sql
--
-- CW-20260929-0019. Six agent_known_skills rows (System Architect, Torque
-- Supervisor) name skills that are not in the skills table and are not in
-- agent-setup's catalog either: they belonged to the retired agent-os registry
-- (archived 2026-08-24). ListAgentSkills joins on skills, so an agent never saw
-- them, and nothing said so.
--
-- This removes those grants, by name, and only where no skill row of that slug
-- exists, so a slug installed later keeps its grant. It leaves two names that
-- need a decision, not a delete: adr (in agent-setup, awaiting install) and
-- capture-to-vanta (successor is ambiguous). Delete-only backfill: no schema
-- change, no rows inserted.

DELETE FROM agent_known_skills
 WHERE skill_name IN (
         'sp-brainstorming',
         'sp-writing-plans',
         'sp-systematic-debugging',
         'sp-verification-before-completion',
         'dispatching-parallel-agents',
         'escalate'
       )
   AND skill_name NOT IN (SELECT slug FROM skills);

-- +goose Down
-- Deleted grants for skills that do not exist are not restored.
SELECT 1;
