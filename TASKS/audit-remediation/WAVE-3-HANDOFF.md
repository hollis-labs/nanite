# Wave 3 handoff — for the next audit-remediation kickoff author

**Audience: a fresh session with zero memory of this wave.** This document
records the code and evidence that survived independent review, the one task
that deliberately did not reach review, and the operator decisions needed to
interpret the resulting baseline. It is not a restatement of the ten task
files' original plans.

Wave 3 closed with **nine tasks reviewed and one task implemented-only**.
Tasks `08/01`–`08/07`, `08/09`, and `08/10` are reviewed. Task `08/08` has a
correct, validated dependency/toolchain update, but its full-repository race
gate was explicitly deferred by the operator after repeated migration-time
suite timeouts; it remains `implemented`, not `reviewed`.

The current Wave 3 rows in `TASKS/INDEX.md` are the status truth used here.
At this handoff's creation, those row changes are still an **orchestrator-owned,
uncommitted working-tree update** awaiting final reconciliation. This document
does not commit or otherwise take ownership of that index change.

---

## 1. What actually shipped

### `08/01` — A2A push-notification webhook SSRF boundary

The mandatory production trace corrected the task's premise before code was
accepted. `POST /api/a2a/jsonrpc` can submit a caller-controlled push URL
without authentication when optional global Basic Auth is unset, and the
supported wide-bind option can make that route remotely reachable. The finding
was therefore corrected to an external-unauthenticated boundary and high
severity/high confidence. The operator approved the narrow SSRF fix without
expanding the task into an A2A authentication redesign.

`A2APushNotifier.processDelivery` is the sole outbound consumer. Its production
client now:

- permits HTTPS only;
- resolves and checks every DNS answer, rejecting the destination if any answer
  is denied;
- dials an approved literal address rather than resolving the hostname again;
- disables connection reuse so a later request cannot inherit an old routing
  decision; and
- treats every redirect as a fresh scheme, resolution, policy, and pinning
  decision.

The original request semantics were retained: the logical URL/Host and TLS SNI
remain the caller's hostname, the payload and authorization header are
preserved, the overall timeout remains ten seconds, and the existing three-attempt
delivery with quadratic pre-terminal backoff remains intact. Test-only resolver,
dialer, HTTP, and localhost seams are unexported and have no production setter.
Fresh review traced the route, caller, dial, redirects, retry/delete behavior,
and production construction rather than accepting the worker narrative.

This task deliberately reused `internal/ssrf`; it did not add another CIDR
table. That coordination is described with `08/09` below.

### `08/02` — dev grep/glob symlink and validation/use confinement

`dev_grep` and `dev_glob` now resolve each walked entry through
`pathsafe.ResolveUnder`, preserving benign symlinks that still resolve inside
the granted root while rejecting links outside it. The subsequent metadata and
content operations are performed through an `os.Root` rooted handle, closing
the gap where a path could be replaced after validation but before use.

The accepted regressions cover an outside symlink, an allowed in-root symlink,
and deterministic file-to-outside-symlink swaps after validation for both grep
and glob. Review mutation-tested the fixture: replacing rooted access with
ordinary `os.Stat`/`os.Open` re-exposed the outside target, proving the tests
guard the actual TOCTOU boundary.

Review also found a separate, pre-existing correctness defect: a first-line
`callGrep` match can evaluate modulo zero before the empty context ring is
checked. The security fixture was moved past a nonmatching first line so this
panic could not create a false pass. The panic was **not** fixed in `08/02` and
is recorded as a bounded follow-up in `TASKS/ESCALATIONS.md`.

### `08/03` — frozen G304 triage and four selected remediation sites

The task did not convert a broad static-analysis cluster into a repository-wide
path rewrite. It refreshed the frozen evidence, classified the production
surface, obtained an operator decision, and changed exactly the selected sites.

The immutable checkpoint
`08-remaining-security-hardening/03-gosec-g304-triage-checkpoint.md` records 70
frozen production G304 coordinates. Six operator-CLI sites and five
`managed_*` sites were excluded, leaving 59 classified sites. The higher-risk
trace covered 35 of those: 16 external-unauthenticated, nine agent-controlled,
and ten catalog-controlled.

The operator selected exactly four source coordinates across three boundaries:

1. `internal/contextbroker/source_pcc.go` — confine PCC scope/entry joins.
2. `internal/skill/parser.go` — confine both the selected `SKILL.md` and every
   package file opened from it.
3. `internal/api/plugins.go` — reject install-local source-tree symlinks before
   copying plugin files.

The parser contributes two source coordinates, producing the four-site total.
The PCC and skill paths use `pathsafe.ResolveUnder`; plugin copy rejects the
selected source-tree symlink case. Tests cover traversal, direct symlinks,
subpath confinement, and plugin copy behavior. `.golangci.yml` contains narrow
`forbidigo` guards for the exact PCC and parser source operations; the plugin
site was intentionally not generalized into that guard. Review reproduced the
70 minus 11 equals 59 accounting and confirmed none of the unselected sites
silently entered scope.

### `08/04` — permission default-mode intent was documentation, not behavior

AD-16 and the full production write trace established that
`permission.Engine.Check(ModeDefault, dev_write/dev_edit)` returning
`{Allow:false, Ask:false}` is intentional. That engine result is not a direct
filesystem authorization.

For the normal tool path, the shared path-grant manager is registered,
propagated through `WithPathGrants`, and consumed by `callWrite`/`callEdit`.
Their path resolution requires either a configured static `AllowedPaths` match
or an applicable session path grant before mutation. `Engine.sessionGrants` is
a distinct cache for tool approvals, not a replacement for the path grants.
The workflow and stdio production constructions that bypass the permission
engine still reach the same write/edit confinement layer.

Only the misleading comment changed. A regression pins the intended
`Allow=false, Ask=false` tuple. Review enumerated the one production
`ModeDefault` engine constructor and the alternate production tool callers,
and found no write bypass or behavior change.

### `08/05` — AgentExec credential-bearing environment keys

The env overlay hardening is intentionally specific to agent-controlled
execution. `AgentExec` retains the existing case-insensitive secret-substring
filter and adds a documented exact-key denial rule for credential-bearing
connection/webhook variables that do not contain the old substrings:

- database/broker endpoints: `DATABASE_URL`, `SUPPORT_DATABASE_URL`,
  `REDIS_URL`, `AMQP_URL`, `ELASTICSEARCH_URL`, `MONGODB_URI`;
- webhooks: `SLACK_WEBHOOK_URL`, `DISCORD_WEBHOOK_URL`; and
- personal access tokens: `GH_PAT`.

The production trace found Workflow `ShellStep` as the only `AgentExec` caller
that supplies an env overlay; code execution and `dev_bash` do not. The shell
API is the sole `UserExec` path and has no comparable overlay producer.
`UserExec` semantics were deliberately left unchanged: its old substring rule
still strips such keys as `GITHUB_TOKEN`, while exact-only names introduced for
the agent boundary remain available there. Tests distinguish those two trust
boundaries instead of turning this into a general secret scanner or allowlist.

### `08/06` — sandbox proxy slow-header protection

The sandbox proxy's `http.Server` now has `ReadHeaderTimeout: 10s`. The focused
test checks that exact value. No other server timeout, proxy policy, request
body handling, or network behavior changed. Review included the focused race
test and a targeted G112 scan.

### `08/07` — local-only server bind posture and operator migration path

AD-15 kept TLS out of scope and made the shipped local-only posture explicit.
`internal/config/appconfig.go` now provides `http.bind_address`, defaulting to
`127.0.0.1`. `cmd/nanite/main.go` exposes
`nanite serve --bind-address <host-or-IP>`; `internal/server` constructs the
listener with `net.JoinHostPort`. Validation accepts host-only ASCII DNS names,
raw IPv4, and unbracketed IPv6, and rejects whitespace, bracketed/host:port
forms, and malformed values before startup logging.

Startup always reports whether Basic Auth is enabled and emits a warning when
it is disabled. Existing optional Basic Auth behavior is otherwise unchanged.
The exact wide-bind migration is documented under `CHANGELOG.md`'s Unreleased
breaking changes:

```text
nanite serve --bind-address 0.0.0.0
```

The checked-in `docker-compose.yaml` makes that opt-in explicitly with
`command: ["--bind-address", "0.0.0.0"]`; combined with the image entrypoint,
the container runs `./nanite serve -db /data/nanite.db --bind-address 0.0.0.0`
and continues to expose port 8090. Plain host and image startup remain
loopback-only. Because a Docker Compose runtime plugin was unavailable, review
validated the YAML shape and final argv with independent YAML/JSON parsing,
and exercised startup from disposable state. Review required a correction for
the initially missing Compose opt-in, stale caller-identity comments, and late
bind validation before passing the task.

### `08/08` — dependency/toolchain vulnerability updates, implemented only

The Go directive moved from 1.26.2 to 1.26.7. The OpenTelemetry sibling
modules were kept aligned at v1.45.0, with the required gRPC gateway,
protobuf, Genproto, and gRPC transitive updates. This was a constrained
vulnerability update, not a broad dependency refresh.

The dependency result itself is clean: `govulncheck ./...`, build, vet, the
full non-race test suite, module verification, and tidy-diff verification
passed; the production `main -> otel.Init` reachability path builds.

The task is **not reviewed**. Two exact full `go test -race ./...` runs emitted
no race warning but exceeded default per-package timeouts while SQLite/Goose
fixtures repeatedly applied migrations. A focused `internal/selftools` race
run also exceeded the default timeout and passed only with a 30-minute timeout,
taking 1,446.675 seconds. The operator explicitly deferred the full-race gate
rather than authorize more prolonged campaigns. `08/08`, `GO-SEC-001`, and
`GO-SEC-002` therefore remain `implemented`; no later document should rewrite
this as a race PASS or a reviewed task.

### `08/09` — project paths, artifacts, plugin UI, and shared SSRF policy

This task closed five findings without creating a general path or networking
framework beyond the required shared policy.

- Project `repo_path` is either empty or a canonical existing directory.
  Filesystem root, the home directory itself, and protected system roots such
  as `/etc`, `/usr`, `/var`, and `/System` are rejected; ordinary home
  subdirectories remain valid. Existing rows are not swept and acquire the
  rule on their next update.
- Artifact placement is confined and stored canonically, with the download
  sink retaining defense in depth.
- Plugin UI file access uses `pathsafe.ResolveUnder` and returns 403 for
  escaping symlinks.
- Plugin lifecycle mutations now share canonical plugin-ID validation across
  API, CLI, install, enable/disable, and uninstall paths; sink-side defenses
  protect outside files even if a caller regresses.
- The catalog downloader retains its two-minute timeout and 100 MiB cap, but
  now rejects denied DNS answers, pins approved literals, revalidates
  redirects, and does not inherit an ambient proxy.

`internal/ssrf` is the narrow shared address policy: `Resolver`,
`DefaultResolver`, `ResolveAndPin`, `IsLocalhostName`, `ErrBlocked`, and one
canonical private/special-purpose CIDR policy. At the end of `08/09` it served
three production consumers: the sandbox proxy, MCP web fetch, and plugin
downloader. `08/01` deliberately became the fourth consumer for A2A delivery.
There is no fourth copy of the CIDR policy in A2A code. Tests exercise the
shared denied ranges, including CGNAT, ULA, and unspecified addresses, plus
all-answer rejection, pinned dialing, and redirect re-resolution.

Fresh review initially found sibling plugin lifecycle traversal, stale caller
mappings, and missing shared-policy range coverage. The correction centralized
plugin ID validation and added adversarial mutation tests that preserve an
outside `plugin.yaml`'s bytes and mode. Only the corrected version passed.

### `08/10` — producer validation parity and correct memory pagination

GO-API-004 and GO-API-005 were treated as separate problems.

For schedules, the current producer trace found HTTP, reflex, and self-tool
producers, with `InsertAgentSchedule` as the final storage boundary. They now
share the applicable schedule validation/canonicalization rule, including
whitespace-only name/body rejection and the `loop_run_tick` case. The
self-tool keeps its deliberately narrower one-shot cron schema. Parity tests
cover producer behavior beyond malformed cron. The planned settings
consolidation premise was false: only `handleUpdateSettings` produces the
named settings enums, while the tools path updates preferences. That scope
correction is recorded; settings code was not redesigned without sibling
producers.

For memories, the production `RecallOpts`/`Recall` callers in context broker,
grounding, learnings, and toolclient retain their zero-value behavior. The API
uses `RecallPage`, which now examines the uncapped set of metadata-matching
current revisions, applies one Go Unicode-aware search predicate for both page
and total, preserves the applicable Tesseract ranking/chronological order,
computes the real filtered total, and only then applies offset and the per-page
limit (still capped at 500). Regressions include more than 500 higher-ranked
nonmatches, status-only and large offsets at or above 500, and non-ASCII case
folding. Legacy timestamps parse both RFC3339Nano and `time.DateTime` and are
covered in ordering/ranking tests.

The pre-existing list-read reinforcement behavior is preserved: only records
on the returned page receive Tesseract's access-count and last-accessed update.
Filtered-out, before-offset, and after-page records do not. Tests verify those
side effects independently of total and filtering.

#### `08/10` test-isolation incident

An early pagination regression used a temporary Nanite database but silently
opened the operator's independent Tesseract database at
`/Users/chrispian/.local/share/tesseract/workspaces/default/main.db`. Repeated
and concurrent runs wrote 515 synthetic logical memories and 4,655 revisions/
FTS entries, then collided with `SQLITE_BUSY`. Service `NewContainer` fixtures
could reach the same global database and start its write-capable decay job.

The operator approved and the orchestrator performed exact cleanup; workers
and reviewers did not touch unrelated user data. Before deletion, an
integrity-checked backup was created at:

```text
/Users/chrispian/.local/share/tesseract/workspaces/default/main.db.pre-wave3-test-cleanup-20260823-0042
```

One transaction deleted exactly the 515 identified synthetic state rows.
Foreign-key cascades and FTS triggers removed the 4,655 matching revisions and
index entries. Post-cleanup checks found zero matching state rows, zero
matching revisions, zero foreign-key violations, and
`PRAGMA integrity_check = ok`. Corrections `d0ff47e9` and `85931355` isolate
API, memory, and service tests under disposable HOME/XDG/Tesseract roots and
assert SQLite's actually opened `main` path through `PRAGMA database_list`.
Final fresh review confirmed those tests no longer open the operator database.
The backup remains intentionally retained until the operator chooses its
normal retention outcome.

## 2. Decisions and scope corrections applied

- **A2A auth-boundary escalation:** the URL is externally unauthenticated when
  Basic Auth is unset. The operator approved the same narrow SSRF remediation,
  corrected finding metadata, and explicitly rejected turning `08/01` into an
  authentication redesign.
- **AD-15:** keep TLS out of `08/07`; default to loopback, provide explicit
  bind configuration/CLI migration, and make authentication posture visible.
- **AD-16:** `{Allow:false, Ask:false}` is the intended permission-engine
  default for non-destructive write/edit. Static allowed paths and session
  path grants remain the real filesystem authorization layer.
- **AD-27:** use the bounded canonical project-root rule described above,
  applied on create/update rather than by migrating existing rows.
- **AD-28:** centralize the DNS/IP policy in `internal/ssrf`; pin resolved
  addresses and revalidate redirects instead of duplicating CIDRs per caller.
- **G304 selection:** remediate only the four frozen coordinates the operator
  selected. The optional agent-profile/envelope-validator candidates stayed
  unselected; dev-tool paths remained with `08/02`; workspace repo-root policy
  remained with `08/09`.
- **Settings scope correction:** the presumed sibling settings producers do
  not exist. Only schedule validation was consolidated across real producers.
- **`08/08` race disposition:** dependency correctness is accepted as
  implemented; the full-race acceptance gate is deferred, not passed.

## 3. Aggregate validation at wave close

The final merged-tree baseline passed:

- `go build ./...`
- `go vet ./...`
- `go test ./...`

Those commands are the non-race repository close gate. There was **no passing
full-repository race verdict** for Wave 3. Nine reviewed tasks ran proportional
focused race checks where useful, but `08/08`'s full race requirement remains
deferred for the migration-time reason above.

High-signal independent checks for anyone taking this wave as a dependency:

```bash
# Shared SSRF policy and all four production consumers.
grep -R "ResolveAndPin" internal/ssrf internal/sandbox internal/mcp \
  internal/plugin/install internal/service
go test -race ./internal/ssrf -count=1
go test -race ./internal/service -run '^TestA2APushNotifier' -count=1
go test -race ./internal/plugin/install \
  -run '^TestDownload_(BlocksPrivateAndIMDSDestinations|PinsValidatedDNSAnswer)$' -count=1

# Dev-tool entry confinement and deterministic validation/use swaps.
go test -race ./internal/mcp \
  -run '^TestDev(Grep|Glob)_.*Symlink' -count=1

# Frozen G304 accounting and selected boundary regressions.
go test ./internal/contextbroker ./internal/skill ./internal/api -count=1

# Permission tuple and downstream write/edit confinement.
go test ./internal/permission ./internal/mcp -count=1

# AgentExec/UserExec key-policy distinction.
go test -race ./internal/sandbox \
  -run '^Test(BuildAgentEnv_FiltersKnownCredentialNames|FilterSecrets_UserExecSemanticsUnchanged)$' -count=1

# Proxy header timeout and bind/config migration behavior.
go test -race ./internal/sandbox -run '^TestProxy_ReadHeaderTimeout$' -count=1
go test -race ./internal/config ./internal/server ./cmd/nanite \
  -run 'Test.*(Bind|HTTPConfig)' -count=1

# Schedule parity, uncapped filtering, legacy order, reinforcement, and store isolation.
go test -race ./internal/api \
  -run '^Test(SchedulesAPI_|ListMemories_|NewTestAPI_Tesseract)' -count=1
go test -race ./internal/memory -run '^TestMemoryRecallPage_' -count=1
go test -race ./internal/service \
  -run '^TestNewContainer_TesseractDBIsPackageTempIsolated$' -count=1

# Repository close gate (non-race).
go build ./...
go vet ./...
go test ./... -count=1
```

Do not run broad API/memory/service tests until their active revision visibly
contains the disposable Tesseract setup and `PRAGMA database_list` assertion.
The accepted Wave 3 revision does; this warning exists to prevent an old branch
or cherry-pick from repeating the operator-database incident.

## 4. Carry-forward items and preserved limits

1. **`08/08` full-race verdict:** create a practical test-infrastructure path
   for migration-heavy packages—pre-migrated disposable fixtures, deliberate
   serialization/sharding, or an explicit extended budget—then run the gate.
   No additional dependency bump is required by the deferral.
2. **Tesseract cleanup backup:** retain
   `main.db.pre-wave3-test-cleanup-20260823-0042` until the operator makes an
   explicit retention decision. The active database was cleaned and verified;
   this is backup lifecycle, not incomplete data cleanup.
3. **Pre-existing `callGrep` first-line panic:** check `ringLen` before modulo/
   indexing, then add `context=0` and first-line/default-context regressions.
   This is a narrow correctness follow-up, not a reopening of `08/02` security
   confinement.
4. **Index reconciliation:** commit or otherwise reconcile the orchestrator's
   pending Wave 3 `TASKS/INDEX.md` status update. Do not downgrade `08/08` to
   reviewed or describe its race gate as passing.
5. **Operator-configured skill loader:** `08/02`'s sibling enumeration found
   `internal/toolclient/skills.go`'s `LoadSkillsFromDir`. It is not an MCP tool
   entry point and was correctly left outside the task; retain it as a separate
   review surface if that loader's trust model is revisited.
6. **Hand-rolled path checks:** `08/09` explicitly suggested a future grep for
   remaining `Clean` plus `HasPrefix` confinement patterns. That sweep was not
   part of Wave 3 and no absence claim was made.

## 5. Wave 3 status matrix

| Task | Findings | Final status |
|---|---|---|
| `08/01` | GO-SVCCORE-004 | reviewed |
| `08/02` | GO-MCPTOOL-008 | reviewed |
| `08/03` | GO-SEC-003 | reviewed |
| `08/04` | GO-SEC4-003 | reviewed |
| `08/05` | GO-SEC4-004 | reviewed |
| `08/06` | GO-SEC4-008 | reviewed |
| `08/07` | GO-RUNTIME-002 | reviewed |
| `08/08` | GO-SEC-001, GO-SEC-002 | implemented — full race gate deferred |
| `08/09` | GO-API-001, GO-API-002, GO-API-003, GO-API-008, GO-SEC4-007 | reviewed |
| `08/10` | GO-API-004, GO-API-005 | reviewed |

**Count: nine reviewed; one implemented-only; zero validated, in-progress,
blocked, or not-started within Wave 3.** The repository-wide development freeze
under AD-24 remains in force until the operator changes it; this handoff does
not authorize unrelated work.
