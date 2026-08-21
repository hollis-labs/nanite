# Plugin Conformance Harness

A follow-up architecture topic from the harness audit: executable contract tests that launch a real plugin binary and verify handshake/versioning, identity/manifest consistency, registrations, typed errors, shutdown/idempotency, disconnect handling, forbidden capabilities, malformed-output isolation, and cleanup. Possible future surface: `nanite plugin verify`.

**Depends on [09-plugin-system.md](09-plugin-system.md)'s target capability model, currently queued in `TASKS/plugin-system/` (tasks `04`–`06`).** The "forbidden capabilities" portion of this harness has no capability-grant/deny machinery to test against until that batch lands — building it earlier would mean testing behavior that doesn't exist yet.

## What's testable now, independent of that dependency

The subprocess plugin protocol (`internal/plugin/subprocess/`: `protocol.go`, `plugin.go`, `manager.go`, `transport.go`) is real and has typed machinery worth conformance-testing today:

- **Handshake/versioning, identity/manifest consistency, registration** — the `plugin/init`/`plugin/load` exchange and the pre-flight validation pass `TASKS/plugin-system/01` is landing (conflict/collision checks + `NaniteCompat` version-range enforcement, run before handshake).
- **Typed errors** — RPC error codes exist and are mapped (`mapRPCError`), already unit-tested (`TestMapRPCError`). A conformance harness can assert against this real machinery, not a stub.
- **Shutdown/idempotency, disconnect handling, cleanup** — process lifecycle lives in `manager.go`; `UnloadPlugin`'s rollback path is already exercised for load-failure cases.

## The real gap: nothing exercises a real spawned binary end-to-end

`internal/plugin/subprocess/plugin_test.go`'s full-lifecycle test (`TestSubprocessPlugin_LoadLifecycle`) runs init→load→register→unload over an `io.Pipe()` with a mocked handler map — no real subprocess. `manager_test.go` does spawn real processes, but only dummy targets (`/bin/sh`, `/bin/cat`) with no RPC surface — process lifecycle only, protocol never exercised against something real. No fixture plugin exists to conformance-test against: `plugins/` holds only a remote-install catalog (`repos.yaml`), not an installed plugin. The closest starting point is `internal/plugin/scaffold/templates/subprocess/main.go.tmpl` — a generator template, not a built/checked-in fixture.

**Malformed-output isolation** has a partial analog, not the general case the follow-up topic asks for: `internal/plugin/envelope_validator.go` validates envelope-card JSON specifically, not arbitrary RPC payloads.

## Target design

Build a real fixture subprocess plugin (start from the existing scaffold template) as a conformance-test target — not a mock, a compiled binary the harness actually spawns. A conformance suite drives it through:

1. Handshake/versioning, identity/manifest consistency, registration conflict handling — testable now.
2. Typed error paths (`mapRPCError`'s existing vocabulary) — testable now.
3. Shutdown/idempotency, disconnect handling (kill mid-call, malformed response mid-stream), cleanup — testable now, needs a real-process harness rather than `io.Pipe()`.
4. Malformed-output isolation, generalized beyond envelope cards — testable now, needs new coverage (`envelope_validator.go`'s scope is too narrow today).
5. Forbidden-capability attempts (a plugin trying to reach a resource it wasn't granted) — **blocked on `TASKS/plugin-system/04`–`06`** landing the capability manifest schema, install-time approval gate, and RPC-proxy enforcement this test needs to exist against.

Possible future surface: `nanite plugin verify`, alongside the existing `new/install/uninstall/update/logs/reload/watch/release` subcommands (`cmd/nanite/plugin_cmd.go`).

## What's genuinely still open

- Sequencing: items 1–4 above can be built as soon as the fixture plugin exists, without waiting on the capability-model batch. Item 5 cannot start until `TASKS/plugin-system/04`–`06` land.
- No task breakdown exists yet — this doc documents the target and its dependency; planning is separate, out of scope for this architecture session.
