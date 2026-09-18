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

Nanite is headless: this repo is the API and CLI. The GUI — first-run setup
(one click to use `claude`/`codex` if either is found on `PATH`, otherwise an
Anthropic/OpenAI API key with a model picker), Settings, and the chat
surface — lives in the separate [`flux`](https://github.com/hollis-labs/flux)
repo, which talks to this server's API. Without it, `-dev` above just serves
a placeholder shell at `/`.

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
