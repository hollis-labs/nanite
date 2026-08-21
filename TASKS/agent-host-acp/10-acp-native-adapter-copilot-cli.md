# Native ACP adapter — GitHub Copilot CLI

**Phase:** 3 — ACP client abstraction & native adapters (`TASKS/agent-host-acp`)
**Status:** not-started
**Depends on:** `08`
**Touches:** new `adapters/copilotacp/` (or similar) in `libs/go-agent-wrapper`. Repo:
`libs/go-agent-wrapper` (sibling, NOT Nanite).

## Context

`docs/engineering/architecture/17-acp.md`: "GitHub Copilot CLI's `--acp` (stdio + TCP
transports, public preview since 2026-01-28)" — the second native-ACP provider, and the only
one in this batch's scope that genuinely exercises the `Transport` axis of task `02`'s split
(both `stdio` and `tcp` are real, documented options for this specific provider — a good real
proof that the Protocol/Transport split is load-bearing, not just theoretical).

Same posture as task `09`: settled shape, low risk, per the doc's own decision matrix. Same
caution applies — re-verify Copilot CLI's actual current `--acp` behavior directly at
implementation time; this specific external-tool claim wasn't independently re-verified by
this planning session's own code-level research (which focused on the two Hollis-owned
repos).

Neither Anthropic nor OpenAI ships native ACP support directly (per 17-acp.md, OpenAI has an
open, unresolved feature request — `openai/codex#9085`) — Copilot CLI and OpenCode are the
only two providers in this batch's scope with genuinely native ACP support; everything else
(Claude, Codex, Pi) needs a bridge, which is Phase 4's job.

## What to do

1. Confirm Copilot CLI's real current `--acp` invocation and both transport modes (stdio,
   TCP) directly against its own current documentation/`--help` output.
2. Implement a new `adapters.RuntimeAdapter` — thin passthrough, no bridge — that can launch
   Copilot CLI in `--acp` mode over **either** transport, exercising task `02`'s `Transport`
   field as a real, functioning choice (not a single hardcoded value).
3. Produce a `Descriptor{Protocol: ProtocolACP, Transport: <stdio or tcp, per how it's
   launched>, Interrupt: <real, verified value>}`.
4. Confirm `session/update` events map onto `runtimeevents` correctly for a real Copilot CLI
   ACP session.

## Done means

- A working Copilot-CLI-driving ACP adapter exists, implementing task `08`'s abstraction,
  supporting both `stdio` and `tcp` transport modes — at minimum one real end-to-end test per
  transport, not just one.
- A real (not mocked) Copilot CLI ACP session, launched and driven through this adapter,
  completes at least one real turn with correctly-translated `runtimeevents` activity.
- The adapter's real `Interrupt` capability is verified directly, not assumed.
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.
