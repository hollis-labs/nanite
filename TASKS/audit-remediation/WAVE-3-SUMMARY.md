# Wave 3 summary — for the operator

Wave 3 of audit remediation closed with **nine tasks reviewed and one task
implemented-only**. It hardened four outbound SSRF consumers behind one
shared policy, filesystem reads and artifact/plugin paths, agent environment
filtering, proxy and server exposure defaults, schedule validation, and memory
pagination. The dependency/toolchain remediation in `08/08` is implemented and
clean on its non-race gates, but remains deliberately unreviewed because the
operator deferred its migration-heavy full-race gate.

The current Wave 3 rows in `TASKS/INDEX.md` are the status truth summarized
below. Those edits are still an orchestrator-owned, uncommitted working-tree
update awaiting final reconciliation; these two documents do not take
ownership of that change.

---

## What shipped

### One shared SSRF policy, including the externally unauthenticated A2A path

The A2A premise changed materially under the mandatory trace. With optional
Basic Auth unset, `POST /api/a2a/jsonrpc` can submit a push-notification URL
without a per-route identity check, and the supported wide bind can expose it
remotely. The finding is now external unauthenticated and high severity/high
confidence. The operator approved the narrow SSRF fix and explicitly kept A2A
authentication redesign out of scope.

`A2APushNotifier.processDelivery`, its sole outbound consumer, is HTTPS-only,
checks every resolved answer, rejects if any answer is denied, dials an
approved literal, and freshly resolves/pins every redirect. Host, TLS SNI,
authorization, payload, ten-second timeout, three-attempt delivery, backoff,
and terminal cleanup behavior remain intact. Injectable resolver/dialer and
HTTP/localhost exceptions are unexported test seams with no production setter.

The policy is not duplicated. `08/09` introduced narrow `internal/ssrf` policy
shared by sandbox proxy, MCP web fetch, and plugin download; `08/01` became its
fourth production consumer. The shared tests cover denied private and
special-purpose ranges, including CGNAT, ULA, and unspecified addresses, as
well as all-answer denial, literal pinning, and redirect re-resolution.

### Filesystem trust boundaries were narrowed at selected, evidenced sites

`dev_grep` and `dev_glob` now confine every walked entry while preserving
in-root symlinks, and use rooted filesystem handles to close the
validation/use swap. Review required deterministic symlink-swap tests and
mutation-tested them against ordinary unrooted access.

The frozen G304 triage counted 70 production coordinates, excluded six
operator-CLI plus five `managed_*` sites, and classified the remaining 59.
The operator's four-site selection was exact: one PCC join, two skill parser
opens, and one plugin source-tree copy. Only those four source coordinates were
remediated, with the checkpoint retained as immutable evidence and narrow lint
guards for the PCC/parser operations.

Project repo paths, artifact placement, plugin UI reads, and plugin lifecycle
IDs are now canonicalized and confined. Review expanded the initially narrow
plugin fix only after proving enable/disable/uninstall siblings reached the
same sink; adversarial tests preserve an outside file's bytes and mode.

### Permission and environment boundaries now match their actual intent

AD-16 confirmed that `ModeDefault` returning `{Allow:false, Ask:false}` for
`dev_write`/`dev_edit` is correct: the engine is not the filesystem gate.
Static allowed paths or session path grants are enforced downstream before
write/edit mutation. `Engine.sessionGrants` remains a separate tool-approval
cache. Only the comment and a tuple-pinning test changed; fresh review found no
behavioral rewrite or alternate production bypass.

`AgentExec` now additionally denies exact credential-bearing endpoint,
webhook, and PAT names that the old substring heuristic missed:
`DATABASE_URL`, `SUPPORT_DATABASE_URL`, `REDIS_URL`, `AMQP_URL`,
`ELASTICSEARCH_URL`, `MONGODB_URI`, `SLACK_WEBHOOK_URL`,
`DISCORD_WEBHOOK_URL`, and `GH_PAT`. The only production env-overlay caller is
Workflow `ShellStep`. `UserExec` behavior remains unchanged and is explicitly
tested as a different trust boundary.

### Network exposure now has safe defaults and an exact migration path

The sandbox proxy now sets a ten-second `ReadHeaderTimeout`, with no broader
timeout or proxy-policy changes.

The server now defaults `http.bind_address` to `127.0.0.1` and exposes
`nanite serve --bind-address <host-or-IP>`. Startup reports the Basic Auth
state and warns when auth is disabled. TLS remains out of scope under AD-15.
Operators who need wide binding have an exact documented migration:

```text
nanite serve --bind-address 0.0.0.0
```

The release note is in `CHANGELOG.md`. The config and CLI wiring live in
`internal/config/appconfig.go` and `cmd/nanite/main.go`. Checked-in
`docker-compose.yaml` explicitly opts into `--bind-address 0.0.0.0`, preserving
the container's port-8090 reachability, while plain host/image startup stays
loopback-only. Review required corrections for the initially absent Compose
opt-in, stale caller-identity comments, and validation timing before PASS.

### API semantics were consolidated only where real producers exist

HTTP, reflex, self-tool, and the final schedule insert boundary now share the
applicable schedule validation/canonicalization, including whitespace-only
name/body rejection and `loop_run_tick`; the self-tool retains its narrower
one-shot cron schema. The planned settings consolidation was not performed
because the producer trace disproved its premise: only the HTTP settings
handler produces the named enums, while the tools path changes preferences.
That is a recorded scope correction, not missing work.

Memory API pagination now filters the uncapped set of current revisions before
offset/limit, calculates a true filtered total, uses the same Go Unicode case
predicate for page and total, preserves Tesseract order, and keeps the 500
maximum only as a page-size cap. Regressions exceed 500 nonmatches and cover
large offsets, Unicode case, legacy `time.DateTime` timestamps, current-revision
selection, and ranking. Only returned records receive the existing
access-count/last-accessed reinforcement; filtered and off-page records do not.
Zero-value non-API recall callers remain unaffected.

### Dependency remediation is correct but not fully accepted

Go moved from 1.26.2 to 1.26.7; OpenTelemetry siblings are aligned at v1.45.0
with only required transitive updates. `govulncheck`, build, vet, full non-race
tests, module verification, and tidy verification passed.

Two full race runs produced no data-race warning but timed out while repeated
SQLite/Goose migrations ran under race instrumentation. A focused self-tools
race run passed only with a 30-minute timeout after 1,446.675 seconds. The
operator explicitly deferred further campaigns. Therefore `08/08`,
GO-SEC-001, and GO-SEC-002 remain `implemented`, not `reviewed`; Wave 3 does
not claim a passing full race suite.

## Incidents, deviations, and review outcomes

- **A2A boundary correction:** the route is weaker than the task assumed.
  Operator resolution preserved the bounded SSRF fix, corrected severity and
  trust classification, and excluded an auth redesign.
- **`08/02` pre-existing panic:** review found first-line `callGrep` matches
  can divide by zero in the empty context-ring calculation. The security test
  was repaired so it could not false-pass. The panic remains a narrow
  correctness follow-up in `TASKS/ESCALATIONS.md`.
- **Four-site G304 selection:** the broad cluster was not mechanically fixed.
  The operator chose the PCC site, two parser sites, and plugin source-tree
  site from the frozen checkpoint; all others remained deliberately outside
  this task.
- **`08/07` review corrections:** Compose exposure, caller-identity comments,
  and early bind validation were incomplete initially and fixed before PASS.
- **`08/09` review corrections:** sibling plugin lifecycle mutations and
  missing policy-range tests were real gaps in the first implementation and
  were corrected before PASS.
- **`08/10` required two correctness rounds:** review found a finite 500-row
  prefilter, Unicode page/total predicate drift, schedule whitespace drift,
  missing legacy timestamp behavior, and lost list-read reinforcement. All
  were corrected and independently re-reviewed.

### `08/10` operator-database test contamination — closed and cleaned

An early pagination regression redirected Nanite's database but not
Tesseract's independent XDG store. Repeated/concurrent runs wrote 515 synthetic
logical memories and 4,655 revisions/FTS entries into the operator database,
then hit `SQLITE_BUSY`. Service `NewContainer` fixtures could reach the same
store and start its decay job.

The operator approved exact cleanup. The orchestrator first created and
integrity-checked this backup:

```text
/Users/chrispian/.local/share/tesseract/workspaces/default/main.db.pre-wave3-test-cleanup-20260823-0042
```

One transaction removed exactly the 515 identified state rows; cascades and
FTS triggers removed all 4,655 matching revisions/index entries. Verification
found zero matching state/revision rows, zero foreign-key violations, and
`PRAGMA integrity_check = ok`. Corrections `d0ff47e9` and `85931355` isolate
API, memory, and service tests under disposable HOME/XDG/Tesseract roots and
assert the actual opened SQLite `main` path using `PRAGMA database_list`.
Final fresh review did not open the operator DB. Keep the backup until the
operator chooses its retention outcome.

## Verification at close

The final merged baseline passed:

- `go build ./...`
- `go vet ./...`
- `go test ./...`

These are non-race repository checks. Focused race tests passed across reviewed
subsystems where proportionate. The full-repository race gate did **not** pass;
it is the explicit `08/08` deferral above.

## Items needing follow-up

1. Establish a practical full-race fixture/runtime path for migration-heavy
   packages, then complete `08/08` review. Do not infer another dependency
   change is needed.
2. Retain the Tesseract cleanup backup until the operator makes an explicit
   retention decision.
3. Fix the pre-existing `callGrep` empty-ring modulo order and cover first-line
   matches with `context=0` and default context.
4. Reconcile and commit the orchestrator-owned `TASKS/INDEX.md` status update
   without changing `08/08` from implemented to reviewed.

## Wave 3 status

| Task | Findings | Status |
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

**Count: nine reviewed; one implemented-only; zero other Wave 3 statuses.**
The repository-wide development freeze remains in force under AD-24 until the
operator changes it.
