# Nanite

Nanite is an agent runtime and chat interface: a Go backend with an embedded
React/shadcn UI that boots CLI coding agents (Claude, Codex, OpenCode,
Copilot, Pi) into a project, executes tools directly, and extends through
hot-loaded, Ed25519-signed subprocess MCP plugins. It ships as a single
binary — `nanite serve` — and runs as a single-user desktop application, not
a multi-tenant service.

> **Pre-release.** Nanite is unreleased, not deployed, and has no outside
> consumers. It's being built in the open: the code, the docs, and this
> README describe what exists today, not a pitch for what's planned.
> Interfaces and behavior change without notice, and there are no
> compatibility guarantees yet.

## What it is today

- **Provider-agnostic runtime.** Native adapters for Claude (streaming stdio)
  and Codex/OpenCode (subprocess-per-turn), plus ACP support for Claude,
  Codex, OpenCode, Copilot, and Pi. Swapping providers doesn't change how a
  session, its context, or its history are handled.
- **An MCP host, both directions.** Nanite is an MCP *client* — it reaches
  external MCP servers over stdio, SSE (2024-11-05), or streamable HTTP
  (2025-06-18) — and an MCP *plugin host*: plugins install as hot-loaded
  subprocesses with signature-verified binaries, not trusted-by-default code.
- **Agent Workflows** run on the shared `go-workflow` engine: one SQLite-backed
  host for launches, approvals, cancellation, scheduling, and multi-agent
  hand-offs, with immutable definition revisions. See
  [`docs/architecture/workflow-engine.md`](docs/architecture/workflow-engine.md).
- **Secure-by-default posture.** The HTTP server binds to loopback unless you
  opt in to `0.0.0.0`, auth state is logged explicitly at startup, and the
  public runtime feed stores metadata only — prompts, tool payloads, and
  model output are never persisted to it because they may contain secrets.

## Where it sits in the stack

```
   models / CLIs        Claude, Codex, OpenCode, Copilot, Pi — swappable
         │
    ┌─────────┐
    │ Nanite  │   session + context + UI, plugin host, MCP client
    └─────────┘
         │
   MCP servers / tools   Tesseract (memory), Torque (tasks), Cerberus
                          (infra/deploy), or any third-party MCP server
```

Nanite doesn't sit *under* or *over* the other Hollis Labs tools — it's a
peer that talks to them the same way it talks to any MCP server. Torque and
Tesseract aren't special-cased; they're just tools it happens to be
configured to reach.

## Examples

**Daily driver.** This is the chat surface Chrispian runs day to day: boot an
agent into a working directory, watch its turns stream in over SSE, and hand
it tools without leaving the session.

**Composition.** A Nanite agent picks up a task from Torque over MCP, does the
work, writes a decision or a follow-up back to Tesseract over MCP, and — when
the change needs to ship — hands off to Cerberus for deploy. Nanite never
talks to Torque's or Tesseract's storage directly; it only ever sees them as
tool calls, so any of the three can be swapped or run standalone.

**Extending it.** A subprocess MCP plugin adds a new tool or data source
(e.g. a vault of internal docs, a ticket system, a custom eval); once
installed and signature-verified, it shows up as ordinary tool calls inside
any session, no core changes required.

## Roadmap

- **Native secure execution.** A sandboxed Python execution path ("Code
  Mode") wired through the production permission/dispatcher, so agents can
  run code directly instead of only calling out to external tools.
- **MCP 2.0.** Nanite currently speaks the 2024-11-05 and 2025-06-18
  transports; moving onto the next MCP spec as it stabilizes.
- **Embeddable workflow engine.** Qualifying `go-workflow` for use outside
  Nanite, so other tools in the stack can adopt the same run/approval model.
- **Public release readiness.** Docs, security review, and release prep are
  ongoing work, not a gate — see [`AGENTS.md`](AGENTS.md) for how that's
  sequenced.
- **macOS packaging.** `make package-release` builds darwin/amd64 and
  darwin/arm64 archives and a `.github/workflows/release.yml` cuts the
  GitHub release on a tag push; a `hollis-labs/homebrew-tap` formula
  (`brew install nanite`) lands with the first tagged release.

## License & Branding

Nanite is open source under the MIT License.

You are free to use, modify, and build on Nanite for personal or commercial
use.

The **Nanite name and branding are protected trademarks**. If you build on
Nanite, you are encouraged to use attribution such as:

- "Built with Nanite"
- "Powered by Nanite"

See [TRADEMARK.md](./TRADEMARK.md) for details.

## Quick Start

```bash
lefthook install                # required once per clone — installs git hooks
go build ./cmd/nanite/
./nanite serve -port 8090 -db ./nanite.db -dev
```

Nanite's built UI includes first-run setup for when no provider is configured
yet: one click to use `claude`/`codex` if either is found on `PATH`,
otherwise an Anthropic/OpenAI API key (kept in the OS keychain) with a model
picker. Skip it and configure a provider later from Settings if you'd
rather. (`-dev` above serves a placeholder shell for the UI — run `cd ui &&
npm run dev` in a second terminal to see it live.)

`lefthook.yml` is tracked, but a tracked config installs no git hooks by
itself. Skip `lefthook install` and the pre-commit format/migration checks
and the `main`-scoped pre-push test run simply never execute. Confirm it
took:

```bash
find .git/hooks -type f ! -name '*.sample'   # expect pre-commit and pre-push
```

Commit time is formatting only, by design. Whole-repo analysis — `go vet`,
scoped `golangci-lint`, and the test suite — lives in `./scripts/check.sh`,
the landing check; run it when a feature lands, not on every commit.

See `docs/` for architecture and demo script. Agent Workflows specifically are
sequenced exclusively by the embedded `go-workflow v0.1.0` module and are
documented in [the engine boundary](docs/architecture/workflow-engine.md) and
[the operator runbook](docs/workflow-operations.md).

Embedded-memory and external MCP operators upgrading to Tesseract v0.10
should follow
[Nanite's Tesseract v0.10 migration guide](docs/tesseract-v0.10-migration.md).

## Admin preferences API

The admin API at `/api/admin/manifest` is off unless both `NANITE_AUTH_USER`
and `NANITE_AUTH_PASSWORD` are configured. Every request requires matching
Basic credentials, including requests from loopback. Caller identity headers
never grant admin access. Follow [the deployment boundary](SECURITY.md) when
exposing Nanite beyond loopback.

The preferences group reads, validates, updates and resets only tool stream
behavior and tool drawer retention. Fetch its schema and revision from the
manifest, then GET `/api/admin/settings/preferences` for the values and strong
ETag. POST `/api/admin/settings/preferences/validate` previews a complete
candidate without saving. Update and reset require that exact ETag in
`If-Match`; a missing precondition returns 428, a stale or weak ETag returns
412, and a changed manifest revision returns 409. Refetch and reconcile intent
before retrying. ETags are opaque revision-qualified generations, not value
hashes; a no-op preserves the ETag and the stored row.

Update accepts `{ "revision": "<revision>", "set": { "tool_stream_behavior":
"hidden" }, "unset": [] }`. Reset accepts `{ "revision": "<revision>",
"keys": ["tool_stream_behavior"] }` and affects only named keys; update's
`unset` has the same keyed-reset semantics. The declared defaults are
`streaming` and `15`. A persisted value equal to its baseline represents no
logical override; another value represents an override. Explicitly setting a
default and resetting therefore produce the same state. This is a canonical
representation policy, not a claim about historical provenance. Changing a
baseline requires a deliberate data/representation migration.

Every POST, including validation, requires exactly one well-formed HTTP(S)
`Origin`, checked before decoding. Use Nanite's actual scheme and authority or
an exact finite `cors_allowed_origins` entry; wildcards and forwarded headers
never grant admin access. CLI clients must also supply an approved Origin
(for example `-H 'Origin: http://localhost:8080'` when addressing that local
HTTP authority). Behind TLS termination, explicitly allow the external HTTPS
Origin in configuration. Admin CORS admits `If-Match` and exposes `ETag`;
legacy route CORS and authentication are independent.

Admin writes are atomic and compare the revision and ETag inside the SQLite
transaction. A changed preference from legacy `PUT /api/settings` invalidates
an admin ETag. A stale whole-row legacy PUT committing **after** an admin
success can still overwrite it: the admin transaction does not repair the
legacy interface's lost updates.

A successful write reports persistence, with unknown apply state, no restart
requirement and no apply targets. It offers no restart/apply action or live
client-cache invalidation. Drawer retention is consumed after a fresh settings
read and a relevant session switch; tool stream behavior has no identified UI
behavior consumer. Saving does not promise an immediate UI effect.

When a process tracker is present, the API also reports a tracked CLI process
count and output inactivity observation. The latter uses the existing
five-minute inactivity heuristic; it does not verify OS process liveness or
whole-application health. Process identities are excluded. A declared provider
that becomes unavailable returns an error rather than a fabricated sample.
