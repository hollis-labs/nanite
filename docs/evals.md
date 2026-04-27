# Interaction-quality eval suite

> I3 / CW-20260420-0028 — Phase 8 Trust & Observability

## Purpose

The eval suite detects regressions in interaction quality across three
benchmark shapes:

| Benchmark | What it measures |
|-----------|-----------------|
| **NoisyToolBench** | Tool-use accuracy under noise — correct tool selected when distractors are present |
| **CAR-bench** | Clarification-asking rate — model asks when user intent is ambiguous |
| **ClarifyMT** | Multi-turn clarification dynamics — model proceeds (no over-asking) once info is supplied |

Phase 8 (v1) is a pilot: one scenario per benchmark, deterministic scoring.
Full dataset integration is v2.

## Running the suite

### Deterministic mode (default, no credentials needed)

```sh
make eval
# or equivalently:
go test -tags eval -race -count=1 -timeout=120s -v ./internal/eval/...
```

The suite uses mock provider responses in unit tests. The mock is wired to
return pass/fail responses that exercise the scorer logic. No network calls are
made.

### Live mode (optional, requires API key)

```sh
NANITE_EVAL_LIVE=1 ANTHROPIC_API_KEY=sk-... make eval
```

Live mode is not implemented in v1. Wiring a real provider is a v2 task (see
follow-up below). The test skips gracefully when the live flag is set but no
provider is wired.

## Why the suite is opt-in

The suite is gated behind the `eval` build tag. Default `go test ./...` skips
it entirely. This keeps CI fast and avoids accidental network calls.

## Scenario format

Scenarios live in `evals/scenarios/<benchmark>/<id>.yaml`. Each file has:

```yaml
id: n01_distractor_tools          # unique, kebab-case
benchmark: noisytoolbench         # noisytoolbench | carbench | clarifymt
description: >
  Human-readable description of what this scenario tests.

user_message: "What's the weather in Austin, TX?"

tools_offered:                    # optional; list of tool stubs the model sees
  - name: get_weather
    description: Returns current weather for a city.
  - name: calculator
    description: Evaluates a math expression.

prev_turns:                       # optional; for ClarifyMT multi-turn scenarios
  - role: user
    content: "Schedule a meeting with the team."
  - role: assistant
    content: "Which team and what time?"

expected_behavior: >
  Human-readable description of the expected response.

rubric:
  # Pick applicable fields:
  asked_clarification: true       # CAR-bench: must ask (true) or must not ask (false)
  called_tool: "get_weather"      # NoisyToolBench: tool name must appear in response
  avoided_tools:                  # NoisyToolBench: these names must NOT appear
    - calculator
  contains_any:                   # any one phrase must appear (case-insensitive)
    - "which"
    - "could you clarify"
  does_not_contain:               # none of these may appear
    - "secret"
```

## Adding new scenarios

1. Create a `.yaml` file in the appropriate `evals/scenarios/<benchmark>/`
   subdirectory (create the subdirectory if the benchmark is new).
2. Run `make eval` — the loader walks the directory recursively, so no
   registration step is needed.
3. If the new scenario covers a rubric shape not yet in `Rubric`, extend
   `internal/eval/types.go` and add scoring logic in `internal/eval/scorer.go`.

## Scoring

Scoring in v1 is binary: a scenario scores **1.0** (pass) only when every
applicable rubric criterion is met. A single criterion failure scores **0.0**.

LLM-as-judge scoring (partial credit, nuanced rubrics) is deferred to v2.

## Follow-ups (v2)

- Full NoisyToolBench, CAR-bench, and ClarifyMT dataset integration.
- Live provider wiring in `cmd/nanite-eval/main.go`.
- Comparative reports across model versions (Phase 6 F5 follow-up).
- Partial-credit scoring via LLM-as-judge.
- CI integration (currently manual `make eval` only).
