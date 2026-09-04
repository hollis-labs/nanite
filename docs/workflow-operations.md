# Agent Workflows operations

## Startup and recovery

Database migrations run before the workflow host is published. Startup then:

1. Ensures the singleton Agent Workflows cutover row exists.
2. Completes the fenced legacy-to-shared cutover described below.
3. Builds the production SQLite state, scheduler, artifacts, StepKind, verifier,
   Loop, Team, and external-framework adapters.
4. Runs a bounded `RecoverActive` pass for exact shared and pilot identities.
5. Starts the normal go-scheduler workers, which deliver later activations.

Recovery is safe to repeat. Runtime generations, claims, wait tokens,
idempotency keys, and persisted child correlations prevent duplicate effects.
An identity or catalog mismatch is a hard error; operators must not bypass it by
changing the stored version or selecting a newer definition.

## One-way cutover

Migration 153 adds a generation-checked control row and an immutable disposition
ledger. The production cutover sequence is:

1. Load the current generation and acquire a time-bounded lease with a unique
   owner and token.
2. Transition `legacy` to `quiescing`. Neither legacy nor shared launches are
   admitted while quiescing.
3. Snapshot every active `nanite_builtin_v1` run. Record an explicit audited
   disposition for each run: drained, canceled, or failed. Nanite never
   fabricates a shared plan for a legacy run.
4. Transition to `shared_only`. The transaction captures late legacy rows and
   refuses the transition while any active or pending disposition remains.
5. Release the lease. Future starts observe `shared_only` idempotently.

If work must be rolled back before step 4, transition `quiescing` back to
`legacy` under the same valid fence. After `shared_only`, rollback means
restoring application and database backups together; do not re-enable the
retired runtime against a database containing shared-host runs.

Lease generation, fence, owner, token, and expiry must all match for mutation.
An expired operator is fenced out even if it later wakes up.

When startup reports pending legacy run IDs, inspect that exact cohort and set
`NANITE_WORKFLOW_LEGACY_DISPOSITIONS` to a JSON array containing one decision
for every reported run and no others. Each decision requires `run_id`, one of
`drained`, `canceled`, or `failed`, plus non-empty `actor` and `reason` fields:

```json
[{"run_id":"run-123","disposition":"canceled","actor":"operator@example.com","reason":"retired during the shared-engine cutover"}]
```

Restart with that plan. Nanite validates the complete cohort before recording
anything, writes immutable disposition audits under the cutover fence, and then
enters `shared_only`. Remove the environment variable after a successful start;
subsequent starts observe the completed state idempotently.

## HTTP and SSE

- `POST /api/workflows/runs` launches graph source on the shared host.
- `GET /api/workflows/runs` and `GET /api/workflows/runs/{runId}` expose the
  compatibility representation for current and pilot runs.
- `POST /api/workflows/runs/{runId}/cancel` performs durable cancellation.
- Approval and callback endpoints resolve one persisted wait with authenticated
  responder provenance and an idempotency key.
- `GET /api/workflows/events` streams durable events. Supply `run_id` to scope
  the stream and `Last-Event-ID` or `after` to resume a cursor.

The default and `engine=go-workflow` select the shared surface.
`engine=hadron` is a supported pilot-client alias. `engine=legacy` permits
historical reads only.

### Team launch idempotency

`POST /api/teams/{teamId}/launch` accepts `Idempotency-Key`. New clients should
generate one stable key before their first attempt and retry the exact same
request with it. A replay returns the original workflow run; reusing a key with
different inputs returns `409 Conflict`. For compatibility, requests without a
key still succeed and Nanite returns the generated key in both the
`Idempotency-Key` response header and `idempotency_key` body field. Such a client
cannot safely recover the key if its connection is lost before receiving the
response, so supplied keys are required for end-to-end retry guarantees.

Team launch, member provisioning, routing installation, signal stand-down, and
wait completion each have durable replay records. Startup and the normal Team
reconciliation cadence repair any committed intermediate state; operators must
not create replacement runs for a returned `workflow_run_id`.

Callback and approval responses use the server's existing HTTP Basic Auth
credentials, `NANITE_AUTH_USER` and `NANITE_AUTH_PASSWORD`. Clients also send
the wait capability in `resume_token` and a stable `Idempotency-Key`. The
authenticated Basic Auth user is persisted as responder provenance and must
match the immutable wait authority. When either credential is unset these
mutation endpoints fail closed; ordinary workflow launch/read/SSE traffic is
unaffected.

### Ambiguous external-framework effects

External Python frameworks execute through the shared runtime's durable
external-operation lane. Nanite first records a prepared outbox request, the
runtime atomically suspends the node with its immutable operation reference,
and only then may the observer mark the receipt pending and start the
subprocess. A completed receipt is replayed without launching another process.
If Nanite restarts while a receipt is pending, the effect is ambiguous and is
never called again automatically.

Authenticated operators list redacted ambiguous receipts with
`GET /api/workflows/external-executions/ambiguous`. The response includes the
receipt, run, node, engine, workflow, request digest, and ambiguity reason; it
never includes params, session identity, or output. After checking the external
framework's authoritative audit log, resolve one receipt with
`POST /api/workflows/external-executions/{receiptId}/resolve`, HTTP Basic Auth,
an `Idempotency-Key` header, and this body:

```json
{"action":"complete_success","output":"authoritative result","reason":"verified provider operation 123"}
```

Use `complete_failure` when the external operation definitively failed. The
actor, action, reason, timestamp, output, and resolution key are persisted.
The host then reconciles the original external operation and continues or
fails its run. Never resolve from guesswork: leaving a receipt ambiguous is
safer than duplicating an external mutation.

## Definition changes

Publish immutable plan material before creating a definition revision. Advance
the named head with its expected generation; retry by rereading on a CAS
conflict. Never overwrite revision bytes, digests, engine identity, or a run's
plan reference. A changed definition is a new revision.

## Validation

Before shipping a host or adapter change, run:

```bash
go test ./internal/workflowhost ./internal/workflowbridge ./internal/store ./internal/service ./internal/api
go test -race ./internal/workflowhost ./internal/workflowbridge ./internal/store ./internal/service ./internal/api
./scripts/check.sh
```

The landing check includes the standalone module import guard and complete
production-factory conformance. Keep the literal `./scripts/check.sh` invocation
in release evidence.
