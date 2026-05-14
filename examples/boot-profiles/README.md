# Example Boot-Profile Catalog

A minimal, self-contained boot-profile catalog the Nanite CLI harness can load
end-to-end without any deferred slot resolvers. Use it to verify the dropdown
surfacing, session-boot, recovery, and stop flows on a fresh Nanite install.

See `docs/boot-profile-cli-harness.md` for the operator-facing walkthrough that
references this catalog.

## Layout

```
examples/boot-profiles/
  boot-profiles/
    claude-smoke.yaml      — profile authored against the Claude CLI adapter
  launches/
    claude-smoke.yaml      — launch pairing for the profile
```

## Wiring it in

Set `boot_profile_catalog_path` in your Nanite config (user-level
`~/.config/nanite/config.yaml` or project-level `./nanite.yaml`):

```yaml
boot_profile_catalog_path: ~/dev/hollis-labs/apps/nanite/examples/boot-profiles
```

Restart `nanite-api`. Reload the chat UI. The provider/model dropdown will list
`Claude CLI (smoke) (boot profile)` alongside the DB-seeded providers.

## What the smoke profile exercises

This catalog deliberately uses **only** the slot source types the pure compiler
resolves at compile time:

- `text` — inline slot body with `{{var}}` substitution.
- `static` — file or glob relative to the catalog root.

The compiled `LaunchSpec` therefore has zero deferred `Requirement` entries and
the chat-resolve layer will hand a fully-rendered boot prompt to `agent.Boot`.

The deferred source types (`cmd`, `http`, `role_summary`, `skill_index`)
currently surface `ErrRequirementUnsupported` at session-boot time — see the
"Known limitations" section of `docs/boot-profile-cli-harness.md`. Avoid them
in any catalog you want to drive a live session.

## Running the smoke

Two paths:

1. **Automated** — `go test ./internal/service/ -run TestBootProfileSmoke_`. The
   smoke test loads this exact directory, exercises the resolve → compile →
   requirements-drain → boot-option-apply pipeline, and asserts the
   `runtimeagent.Options` the harness would hand to `Boot` carry the spec's
   env / args / workdir / boot-prompt. The test does NOT launch a real Claude
   CLI process.

2. **Manual against a live daemon** — follow the operator walkthrough in
   `docs/boot-profile-cli-harness.md`. Requires `claude` on `$PATH` and the
   `nanite-api` service running.
