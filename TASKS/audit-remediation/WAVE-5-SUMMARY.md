# Wave 5 summary — for the operator

Wave 5 is complete as an evidence wave: **three reviewed, zero in progress,
zero blocked**. It did not ship a production decomposition or refactor. It
delivered two reviewed architecture maps, production-door characterization
coverage for the highest-complexity chat path, and one approved no-blanket-
refactor disposition.

The immutable integration point is `fb6527ab`. The final root build, vet, and
non-race test suite passed, and the task-focused race suite passed.

---

## What shipped

### Chat execution (`10/01`)

A new characterization suite exercises `generateResponse` through
`Dispatcher.Run` → `chatRunnerAdapter`, covering plain, tool, provider-error,
compaction/recovery, and both plugin-cancel paths. Focused coverage moved:

- `generateResponse`: 36.7% → 53.7%
- provider-error ranges: 0.9% → 25.2%
- compaction-recovery ranges: 13.5% → 53.2%
- plugin-cancel ranges: 3.8% → 100.0%

The reviewed map reconciles all 52 `chatServiceImpl` fields and all 87 current
production receiver methods, and proposes six incremental `generateResponse`
phases. It preserves the recognizable outer state machine and requires
behavior/race/complexity verification after every future extraction. No
production chat code moved.

### Self-tools and ToolClient (`10/02`)

The reviewed capability map reconciles all 31 `SelfToolsTransport` fields, 82
production receiver methods, and 68 `CallTool` names. It recommends selective
delegation based on real domain cohesion and coupling—not metric reduction—
while retaining the transport as the catalog/dispatch adapter.

`ToolClient` was corrected from its stale audit shape to 20 methods across
three files. Its remaining selection, policy, and execution surface is
coherent; GO-MCPTOOL-007 closes as no action. No selftools or ToolClient code
changed.

### `Container`, Store, and plugin Host (`10/03`)

The current-source review counted `Container` at 64 fields/four production
receiver methods, Store at 372 receiver methods with 32 exact root-package
importers and 61 literal service references, and plugin `Host` at 38 fields/123
receiver methods. Against that evidence, not the older audit counts, the
operator approved the balance now recorded in the AD-14 supplement and Wave 8
task `13/05`:

- no `Container` decomposition;
- no blanket Store split or interface pass without a named consumer showing
  concrete coupling/testability pain; and
- selective Host work only when `GO-PLUGIN-004` identifies a named category
  with concrete ownership, teardown, locking, or testability value. An
  all-category migration sweep is rejected.

No production code changed for this task.

## Operator attention required

AD-12 and AD-13 remain **open by design**. Review status means the evidence is
trusted; it does not approve the proposed boundaries.

- Decide AD-12 against
  `10-architectural-concentration/01-chatserviceimpl-responsibility-map.md`.
- Decide AD-13 against
  `docs/engineering/selftoolstransport-capability-map.md`.

No extraction task may be scoped, created, or dispatched until the relevant
decision is recorded. This preserves the required sequence: map → operator
decision → separately scoped extraction.

## Verification qualification

The full `go test -race ./internal/service/...` run is **not a PASS**. It hit
the known 10-minute SQLite migration timeout with no race report. The
pre-existing `driveBootSession` send-on-closed-channel flake also recurred once
during coverage and passed on retry; it was already durably logged from the
Skills batch.

## Durable record

`TASKS/ESCALATIONS.md` carries the authoritative entries for `10/01`'s
inventory/runtime/wiring-residue corrections, `10/02`'s stale learning-caller
comments, `10/03`'s current metrics and retired grounding example, Wave 8's six
missing decision items, and the earlier `driveBootSession` race. Those entries
are referenced here rather than duplicated.

## Wave 5 status

| Task | Final status |
|---|---|
| `10/01` | reviewed |
| `10/02` | reviewed |
| `10/03` | reviewed |

**Count: three reviewed; zero in progress or blocked.**
