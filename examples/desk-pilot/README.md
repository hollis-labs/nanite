# Live front desk pilot

This opt-in acceptance test uses a real model through a Warden-authenticated
LLM gateway. Normal `go test ./...` skips it; CI must not set `NANITE_DESK_LIVE`.
It creates a temporary copy of `agentdir`, with a 100,000 daily token admission
budget, eight steps per turn and four concurrent workers. In-flight requests can
overshoot the daily admission budget. The test has a five-minute deadline.

Supply an enrolled pilot identity YAML **path**, a gateway endpoint, its wire
(`openai-chat` or `anthropic`) and a model name. The identity's Warden parent
credential must live outside the agent folder. Use a dedicated credential; do
not share it with the Warden CLI while Nanite rotates it. No provider-key fallback
is used. The harness writes `identity.yaml` and `providers.yaml` (`auth: warden`)
only into the temporary agent folder, using these local inputs. It loads and
validates both files before wrapping the selected provider for measurement.
A missing grant fails the test and prints the grant request metadata.

```sh
NANITE_DESK_LIVE=1 \
NANITE_DESK_IDENTITY=/path/to/pilot-identity.yaml \
NANITE_DESK_GATEWAY_URL=https://gateway.example/v1 \
NANITE_DESK_WIRE=openai-chat \
NANITE_DESK_MODEL=your-model \
go test -v -count=1 -timeout=7m ./examples/desk-pilot
```

The fixed script asks one arithmetic question inline, then makes two research
requests with two independent local file tasks each. The real desk model must
produce two dispatch plans; rules fallbacks and suggested plans fail acceptance.
Workers use `read_file` and `return_result`. The host exposes only file reads,
subtasks and background tools; there is no shell or network research tool.

A provider wrapper pauses the first subtask request that contains a real tool
result, before sending that next request. The host closes the runner with work
still running, reopens the same directory, calls `RecoverTurns` and desk recovery,
and waits for all four results. A third open checks recovery idempotence.
The test checks exact result values, one delivery of each marker, and serialized
per-chat delivery observers. Independent jobs may finish in any order; submission
order is not a delivery-order promise. This exercises orderly close/reopen, not
process kill or a crash between delivery persistence and host receipt.

Verbose output includes plans, transcript excerpts, numbered deliveries, called
tools, usage and cost. Planning uses the provider's streaming completion path so
its usage is included. Provider-reported cost is printed separately from the
conservative estimate (USD per million tokens: 15 input, 75 output, 1.5 cache read,
18.75 cache write). The estimate is not an invoice. Missing usage fails the test;
it must not be reported as zero-cost success. Keep the full test output as the
run report, including failures.
