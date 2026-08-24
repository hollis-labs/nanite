# Wave 6 summary - for the operator

Wave 6 is complete: **16 reviewed, zero in progress, zero blocked**.
All Wave 6a and Wave 6b rows in `TASKS/INDEX.md` are `reviewed`, and every
finding mapped to `11-semantic-duplication-migration-drift` in
`findings.json` now has `task_status: "reviewed"`.

This wave handled semantic duplication and migration drift first, then
mechanical boilerplate. The important distinction: it did not treat
duplication as a blanket line-count problem. AD-19 drove a per-instance choice
between shared implementation, parity tests, explicit deferral, or no action.

---

## What shipped

### Semantic divergence and migration drift

`11/01` moved subagent-completion policy resolution onto the same
`override.Resolve` cascade primitive used by message wake policy. Separate
defaults remain separate, but the merge rule is now shared and tested.

`11/02` consolidated Harness v1 durable-agent start/resume/wake handlers onto
the native durable-agent handlers. The caller trace found no evidence that
Harness v1 durable operations were meant to be a separately implemented
version-stable contract; docs describe them as thin wrappers over the same
services.

`11/06` closed the highest-priority behavior divergence in the folder:
OpenAI streaming no longer emits `EventError` and returns early on malformed
streamed tool-call JSON while Anthropic keeps going. Both providers now use a
shared parser and preserve malformed arguments as `_raw`.

`11/09` applied AD-29's delete decision. The dead client-side elicitation path
and its stale tests/comments were removed, leaving only the live Nanite-owned
server-side elicitation route.

`11/11` preserved intentionally independent `dispatch_to_agent` evaluators and
added a real parity test over the shared row-interpretation rules.

### Shared plumbing and mechanical cleanup

`11/05` extracted the MCP post-`CallTool` tail into `processCallToolResult`.

`11/10` introduced `internal/envelopewiring.InstallSharedRegistry` so the
composition root wires the chat, envelope, and plugin registry holders through
one call.

`11/03` added `internal/structuredmessage.UnwrapText` for the shared
StructuredMessage-shaped JSON text unwrap rule.

`11/04` exported `inspector.TrafficLight` and removed the duplicate service
copy.

`11/08` completed AD-20's bounded rename:
`config.RuntimeConfig` and `config.TunablesConfig`.

`11/12` removed the duplicate `toolclient.DevServerName` constant.

`11/14` added `LoadEmbeddedManifest` and `BasePlugin`, then migrated the four
CLI adapter plugins that actually shared the boilerplate.

`11/15` moved the three `*API` decode bypasses onto `a.decode`, added malformed
JSON coverage for them, and explicitly accepted/deferred the optional
`agent_capabilities.go` CRUD-family abstraction after reconfirming the dupl
pairs.

`11/07`, `11/13`, and `11/16` closed as non-invasive tracking decisions:
SSRF CIDR consolidation remains the reviewed `08/09` work; generic Store scan
loops were deferred after the fresh Store `dupl` count remained 46 and focused
Store checks passed; workflow/dispatch naming collisions remain awareness-only.

## Decisions preserved

- **AD-19:** resolve duplication per instance. Shared implementation for real
  migration drift/boilerplate; parity tests for intentionally independent
  semantics; no LOC-driven refactors.
- **AD-20:** rename both `internal/config` exported structs by role inside the
  same package. Do not rename config files or split packages as part of this.
- **AD-29:** delete dead client-side elicitation code. Do not build a missing
  middleware solely because stale docs described it.

## Verification

Task logs and fresh reviews record passing focused checks plus repeated
baseline runs:

```bash
jq empty TASKS/audit-remediation/findings.json
go build ./cmd/nanite/
go vet ./...
go test ./...
git diff --check
go test -race ./... -count=1
```

The aggregate race command exited 0. W6a fresh reviews passed after small
fixes; W6b fresh reviews by Zeno/Dewey/Linnaeus passed.

Focused coverage added or extended for the highest-risk cases: shared reactor
policy cascade, native plus Harness v1 durable-agent endpoints, provider
stream malformed JSON, dispatch-to-agent evaluator parity, envelope registry
pointer identity, embedded plugin manifest/lifecycle helpers, shared API
decode failures, and structured-message unwrap behavior.

This summary did not rerun the Go suite; it reflects the reviewed task-file
verification and final closeout record.

## Operator attention

`GO-STORE-007` remains deferred by design: the Store scan-loop duplication is
correct readable boilerplate with no current bug attached.

`GO-API-009` remains accepted/deferred after reconfirmation. The real decode
bypasses were fixed; the broad CRUD helper was not worth the abstraction cost
for an informational finding.

`GO-EXEC-003` and `GO-EXEC-004` remain defer/awareness rows. No rename is
recommended without new architect review.

One process escalation was logged in `TASKS/ESCALATIONS.md`: the 2026-08-24
entry **"Wave 6a dispatch assumptions overstated file disjointness and
isolation."** Wave 6a was not as file-disjoint as the kickoff assumed, and the
available worker dispatch edited the shared checkout instead of reliably
isolating writes. For future waves in this environment, serialize write agents
or keep parallel agents read-only/report-only while the orchestrator applies
tracker edits.

Wave 7 is dependency-unblocked by Wave 6 completion. The development freeze
still applies: Wave 7 should not start unless the operator explicitly resumes
the audit-remediation batch.

## Final status

| Task | Final status |
|---|---|
| `11/01` | reviewed |
| `11/02` | reviewed |
| `11/03` | reviewed |
| `11/04` | reviewed |
| `11/05` | reviewed |
| `11/06` | reviewed |
| `11/07` | reviewed |
| `11/08` | reviewed |
| `11/09` | reviewed |
| `11/10` | reviewed |
| `11/11` | reviewed |
| `11/12` | reviewed |
| `11/13` | reviewed |
| `11/14` | reviewed |
| `11/15` | reviewed |
| `11/16` | reviewed |

**Count: 16 reviewed; zero in progress or blocked.**
