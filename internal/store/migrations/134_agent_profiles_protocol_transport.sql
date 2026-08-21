-- +goose Up
-- TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md:
-- per-agent Protocol/Transport selection, the DB-configurable surface
-- docs/engineering/architecture/17-acp.md explicitly calls for ("Per-agent
-- protocol/transport selection belongs in the same DB-configurable surface
-- as the existing agent_context_resolvers pattern, not a new schema axis")
-- and 16-agent-host.md's own Descriptor split (task 02,
-- libs/go-agent-wrapper) names Protocol (claude-stream-json /
-- codex-app-server / opencode-native / acp) and Transport (stdio / tcp /
-- http-sse / pty).
--
-- Shape decision (documented at length in this task's Work Log): a single
-- column pair on agent_profiles, following agent_profiles.runtime_kind's
-- own precedent (migration 117) more closely than agent_context_resolvers'
-- separate-table shape -- there is no genuine per-agent 1:many need here
-- (unlike agent_context_resolvers, where one agent plausibly wants several
-- named context slots), just one scalar "how does this agent's CLI
-- process actually launch" choice per agent, exactly like runtime_kind
-- itself.
--
-- No new agents.runtime_kind value -- this column pair is deliberately
-- orthogonal to it. An agent configured with protocol='acp' still carries
-- runtime_kind='cli' unchanged; internal/runtime/agent/factory.go's
-- useACPProtocol consults THIS column, once runtime_kind has already
-- routed the launch into the CLI runtime package.
--
-- NULL/'' (the default for every pre-existing row -- a plain ADD COLUMN
-- with no DEFAULT starts every row NULL) means "use this provider's
-- existing native protocol," per 17-acp.md's explicit "additive, not a
-- cutover" framing: no agent silently switches to ACP without an operator
-- explicitly writing protocol='acp' onto its row. transport is only
-- meaningful when protocol='acp' (task 10's Copilot CLI adapter supports
-- both stdio and tcp; every native protocol is transport-fixed by the
-- protocol choice itself, so a value here would be redundant/misleading
-- for them) -- enforced at the Go layer (validateAgentMultiAgentFields),
-- matching runtime_kind's own "CHECK here too, Go validation too" belt-
-- and-suspenders precedent from migration 117.
ALTER TABLE agent_profiles ADD COLUMN protocol TEXT
    CHECK (protocol IS NULL OR protocol IN (
        'claude-stream-json', 'codex-app-server', 'opencode-native', 'acp'
    ));

ALTER TABLE agent_profiles ADD COLUMN transport TEXT
    CHECK (transport IS NULL OR transport IN ('stdio', 'tcp'));

-- +goose Down
ALTER TABLE agent_profiles DROP COLUMN transport;
ALTER TABLE agent_profiles DROP COLUMN protocol;
