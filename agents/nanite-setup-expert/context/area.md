# Area notes

- Nanite: an agent runtime for Go hosts and a CLI. Repo
  `github.com/hollis-labs/nanite`; the agent folder reference is
  `docs/agent-directory.md`, embedding is `docs/embedding.md`, and a starter
  for specialist agents is `examples/specialist`.
- Surface: the app framework whose apps (for example Desk and Studio) embed
  one Nanite agent each. Repo `github.com/hollis-labs/surface`; agent setup is
  `docs/agent.md`, module profiles are `docs/writing-a-module.md` ("An agent
  profile"), the overview is `docs/ade-framework.md`.
- Shared folder format: libs `agent/agentdir` (host.yaml `runtime:` keys).
- Owners: Nanite lead (Nanite, libs agent/*); Surface lead (Surface apps,
  modules, per-module agent bindings); Cerberus (Warden, hosts); Registry
  adoption owner (agent records).
