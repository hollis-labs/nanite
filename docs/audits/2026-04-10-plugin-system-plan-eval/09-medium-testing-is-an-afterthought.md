# [Medium] Plan's testing strategy is an afterthought — no test plan, no race coverage, no property tests for the JSON-RPC wire protocol

**Scope:** plan-completeness
**Topic:** plan-completeness / testability
**Date:** 2026-04-10

## Problem

The plan's track gates say things like "`go test ./internal/plugin/...` clean" and "`go test -race ./internal/plugin/...` clean", but there is no explicit test plan — no list of what must be tested, no property or fuzz testing for the JSON-RPC wire format, no concurrency stress tests, no integration tests between host and a real subprocess plugin, no regression tests for the three fix sites in Track A.2, no coverage commitment, and the `plugin-sdk` SDK repo's testing section (§C.5) only provides a `Harness` that bypasses the actual IPC layer.

For a re-architecture of this size on a system where plugin reliability must be 100%, testing-as-gate is insufficient.

## Evidence

Plan §C.5 (testing harness):

> `Harness` wraps a plugin and provides direct method dispatch (no subprocess, no RPC) with mocked InitParams and temp dirs.

This is useful but explicitly bypasses the wire layer. The JSON roundtrip feature at OQ3 is mentioned as an opt-in:

> Include the JSON roundtrip feature per OQ3: `WithJSONRoundtrip(true)` option or `NANITE_PLUGIN_SDK_JSON_ROUNDTRIP=1` env var forces every request/response through a JSON marshal+unmarshal cycle to catch wire-format issues. Off by default for speed.

"Off by default" means most test runs won't exercise the wire format. OQ3 also says the lefthook hook will only run roundtrip tests when `*_test.go` files change — a marshal bug in a non-test file won't be caught until CI runs it.

The plan's Track B gates (§B.13):

- `go build ./...` clean
- `go test ./internal/plugin/... ./internal/chat/...` clean
- `go test -race ./internal/plugin/...` clean (race conditions matter for the unregister paths)
- A compiled-in builtin plugin loads and registers entries via the yaml-authoritative path
- Unload the same plugin and verify all registrations are removed (grep the host's internal maps)
- `GET /api/plugins/registry` returns a response shaped per §B.7
- `GET /api/plugins/events` streams events when a plugin is load/unload-cycled

Missing from the gate list:

- **Concurrent RPC test** — multiple Call() in parallel (finding 02).
- **Transport timeout recovery** — verify a timed-out call doesn't permanently break the transport (finding 03).
- **Panic recovery at event dispatch** — a hook that panics doesn't kill the host (finding 04).
- **Full register-then-unregister for every category** — beyond the four categories UnloadPlugin currently cleans (finding 05).
- **Fuzz tests for the JSON-RPC wire format** — malformed response bytes, oversized payloads, embedded nulls, unexpected fields.
- **Property test for manifest schema validation** — the plan's B.2 JSON Schema has 22+ validation rules; each needs a valid + invalid test.
- **Integration test against a real subprocess** — the plan has Track E doing this end-to-end with giphy but no intermediate test.
- **Cross-platform tests** — plan mentions "windows SysProcAttr" as a sharp edge (§13.8) but gates don't require Windows CI.

Track E (§7) is the only place the plan has a real end-to-end test, and it's the last thing before Track G. That means every bug found in the host core (Track B) or SDK (Track C) or frontend (Track D) has to wait until Track E to be discovered, then loops back into B/C/D for fixing. This is the opposite of test-driven development — integration testing is the final step, not a gate at each track's boundary.

Track B's existing test files:

```
internal/plugin/auto_triggers_test.go
internal/plugin/catalog_test.go
internal/plugin/events_test.go
internal/plugin/filter_test.go
internal/plugin/host_test.go
internal/plugin/keybindings_test.go
internal/plugin/load_type_test.go
internal/plugin/prehook_test.go
internal/plugin/signature_test.go
internal/plugin/triggers_test.go
internal/plugin/subprocess/plugin_test.go
internal/plugin/subprocess/transport_test.go
```

Most of these test the current architecture. The plan doesn't say which tests will be rewritten, which will be deleted, which will be kept verbatim, which are expected to break during the rewrite. The execution agent will be rewriting tests on the fly without a checklist, which means tests will drift toward "whatever passes" rather than "whatever verifies the intent."

## Impact

- Regression risk: the plan's big-bang loader rewrite (B.4) will likely break tests that depend on the current yaml-parsing + constructor-lookup path. Without a test plan, "got the tests to pass" = "deleted the tests that failed."
- Integration bugs pile up until Track E.
- Race conditions around the new Unregister paths are unlikely to be caught without explicit stress tests.
- Wire format bugs (JSON marshal assuming a field shape, omitempty surprises, integer overflow in protocol version) hit the first real plugin instead of being caught in unit tests.
- Post-beta bug reports will be hard to triage because there's no "this test suite verifies the plugin system" checkpoint.

## Recommendation

Add a new §B.0 or §C.0 titled "Test plan" that commits to:

1. **Race-test gate at every track boundary.** Not just B — also C, D, E, G, H. `go test -race ./...` runs as a non-skippable step.

2. **Concurrent transport test** (required by findings 02/03): a test that spawns 10 goroutines all calling `transport.Call` concurrently and verifies throughput matches expected (i.e., the calls overlap, not serialize).

3. **Panic-in-hook test** (required by finding 04): a test event hook that panics, verify the host logs and continues.

4. **Full unregister coverage test** (required by finding 05): for each Register* category, a test that registers something, unloads the plugin, and asserts the registry is empty.

5. **Fuzz the JSON-RPC reader.** `FuzzParseRPCResponse` feeds random bytes into the response parser and verifies no panics and clean errors for malformed input. 5 minutes of fuzzing per CI run.

6. **Subprocess integration tests that don't need a real plugin.** Use a test helper that spawns `go run ./internal/plugin/subprocess/testbin/...` — a minimal Go binary acting as a plugin subprocess for host-side tests. This catches bugs Harness misses.

7. **Make `WithJSONRoundtrip` on by default.** The plan's reasoning for off-by-default is "speed." For a project at beta with reliability as the top priority, slow-but-correct tests win. Off-by-default will rot.

8. **Track-by-track test-update checklist.** For each track, list which existing test files are expected to need changes and what the expected behavior is. Prevents "delete tests that fail" drift.

9. **Add a "no tests were deleted without replacement" audit gate** to Track I.7.

Also recommend a follow-up audit scope `plugin-tooling-and-tests` to run the tooling commands this audit deferred (`go vet`, `golangci-lint`, `staticcheck`, `errcheck`, `govulncheck`, `go test -race`) — explicitly flagged as the next pass, per the skill's scoped-review tooling-deferral rule.

## References

- Plan §B.13, §C.7, §E.7, §G.7, §H.4 — current gate checklists (insufficient)
- Plan §C.5 — Harness description
- Plan OQ3 — JSON roundtrip feature (off by default)
- Findings 02, 03, 04, 05 — specific regressions tests should catch
- `internal/plugin/` — 12 existing test files the plan doesn't address
