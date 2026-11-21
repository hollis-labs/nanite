# Validate Nanite

Use the Go version in `go.mod`. Build the CLI from a standalone checkout
with `GOWORK=off go install ./cmd/nanite`.

## Pinned dependencies

Nanite pins `github.com/hollis-labs/libs/agent/agentdir` at `v0.5.0` and a
Substrate revision whose BOM lists it under `app_libs`. Move both together
and let the pin check confirm they agree:

```sh
GOWORK=off go get "github.com/hollis-labs/substrate@${SUBSTRATE_VERSION}"
GOWORK=off go mod tidy
GOWORK=off go run github.com/hollis-labs/substrate/cmd/substrate pins check --app .
```

Keep the `substrate` version in `project.yaml` equal to the one in `go.mod`.

## Checks

The repository's check workflow runs on pull requests and pushes to `main`.
It runs lint, vet, and vulnerability checks, verifies BOM pins and generated CLI wiring, cross-compiles packages,
runs the race suite and offline example, and scans the tree and history for public hygiene. The
compile step has no artifact upload or deploy job.

```sh
go vet ./...
GOWORK=off go run github.com/hollis-labs/substrate/cmd/substrate pins check --app .
go run github.com/hollis-labs/substrate/cmd/substrate generate -f cmd/nanite/substrate.yaml
git diff --exit-code -- cmd/nanite/substrate_wiring.go
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build ./...
go test -race -count=1 ./...
go run ./examples/embed-host --offline
go run github.com/hollis-labs/substrate/cmd/substrate scan-public --ref HEAD
```

The scanner has no native commit range or since option. CI scans the entire
working tree because legacy history has unresolved findings. To check just the
branch history, replay its snapshots and commit messages into a scratch
repository with an independent root and scan that repository.

```sh
repo=$(git rev-parse --show-toplevel)
scan_dir=$(mktemp -d)
git init -q "$scan_dir"
git -C "$scan_dir" fetch -q --no-tags "$repo" HEAD
git rev-list --reverse --first-parent origin/main..HEAD > "$scan_dir/.git/branch-commits"
parent=
while IFS= read -r commit; do
  tree=$(git rev-parse "$commit^{tree}")
  set --
  if [ -n "$parent" ]; then set -- -p "$parent"; fi
  parent=$(git show -s --format=%B "$commit" |
    git -C "$scan_dir" -c user.name=Scan -c user.email=scan@example.com commit-tree "$tree" "$@")
done < "$scan_dir/.git/branch-commits"
git -C "$scan_dir" checkout -q --detach "$parent"
go run github.com/hollis-labs/substrate/cmd/substrate scan-public --repo "$scan_dir" --ref HEAD
```

The lint and vulnerability commands use versions from the Substrate BOM.
For release validation, also collect coverage across package boundaries.

```sh
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4 run
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
go test -coverpkg=./... -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
awk 'NR > 1 {
  statements[$1] = $2
  if ($3 > 0) hit[$1] = 1
} END {
  for (block in statements) {
    pkg = block
    sub("/[^/]+$", "", pkg)
    total[pkg] += statements[block]
    if (hit[block]) covered[pkg] += statements[block]
  }
  for (pkg in total) printf "%s %.1f%%\n", pkg, 100*covered[pkg]/total[pkg]
}' coverage.out | sort
```

Run the race suite three times with `-count=1` for release validation.
The embedding API golden test runs as part of the suite. For an intentional
API change, regenerate it with
`go test ./agent -run '^TestAPIGolden$' -update-agent-api` and review the diff.

## Fuzz targets

Handlers that take model- or server-supplied input have Go fuzz targets. Their
seeds run as ordinary tests in the suite above. The `nanite fuzz` workflow
fuzzes each target for a minute every night and keeps any failing input as an
artifact. To fuzz one locally:

```sh
go test -run '^$' -fuzz '^FuzzSearch$' -fuzztime 60s ./internal/selftools/
```

| Target | Package | Invariant |
|---|---|---|
| `FuzzSearch` | `internal/selftools` | No panic, under 2s, never returns content from outside the root or from a protected path. |
| `FuzzFindGlob` | `internal/selftools` | Results stay inside the root and off protected paths; a literal glob equal to a file's base name finds it. |
| `FuzzWorkspaceMutate` | `internal/selftools` | read, list, write and edit never touch anything outside the root and never write a protected path in any letter case. |
| `FuzzParseBash` | `internal/selftools` | An accepted call has a non-blank command and a timeout in [1s, 600s]. |
| `FuzzDenylist` | `internal/shell` | No panic, under 1s, a reason exactly when blocked, and still blocked after `true;`. |
| `FuzzMCPTool` | `internal/mcpclient` | An admitted tool's model name matches `^[a-zA-Z0-9_-]{1,64}$`. |
| `FuzzUnpack` | `toolregistry` | Only regular files and directories under the package directory; no links and no setuid, setgid or sticky bits. |
| `FuzzValidate` | `registryagent` | An accepted payload is an object with a supported schema, decided within 3s. |
| `FuzzMaterializeProfile` | `registryagent` | Materialize returns for any agent/1 or launch_profile/1 payload. |

A failing input is written under the package's `testdata/fuzz/<Target>/`.
Commit it with the fix so the seed keeps guarding the regression.

## Live model test

The default run skips the live test. Set `NANITE_GATEWAY_URL` and
`NANITE_GATEWAY_KEY_REF` for your gateway, then run the test below. To use
OpenAI, set `NANITE_LIVE_PROVIDER=openai` and set
`NANITE_DEFAULT_MODEL` to a compatible model.

For local testing without a gateway, set `NANITE_PROVIDER_KEY_ENV=1` and
`ANTHROPIC_API_KEY` instead. The switch is off by default; a key in the
environment is never read without it. A key that is not scoped to a
workspace also needs
`ANTHROPIC_CUSTOM_HEADERS='anthropic-workspace-id: <workspace id>'`.

```sh
NANITE_LIVE_TEST=1 go test ./cmd/nanite -run '^TestLiveGatewayToolTurn$' -count=1 -v
```

It opens a temporary agent, verifies one real `read_file` tool call, and
checks the model's reply for the fixture content.

The Code Mode evals have a live run too. It uses the same credentials, spends
money under a dollar cap, and never runs in CI. See
[evals/code-mode.md](evals/code-mode.md):

```sh
go run ./cmd/nanite-eval -live -model claude-sonnet-5-5 -max-usd 10 -turn-tokens 150000 -out docs/evals/live-YYYY-MM-DD
```

## Manual end-to-end check

Start at the repository root. For the model turns, configure credentials and
optional gateway settings as described in [agent-directory.md](agent-directory.md).

```sh
go run ./examples/embed-host --offline
demo_dir=$(mktemp -d)
go build -o "$demo_dir/nanite" ./cmd/nanite
"$demo_dir/nanite" init "$demo_dir/agent"
"$demo_dir/nanite" validate "$demo_dir/agent"
(
  cd "$demo_dir/agent"
  printf 'Nanite validation fixture\n' > fixture.txt
  "$demo_dir/nanite" -p 'Use read_file to read fixture.txt and report its content.' --output json
  "$demo_dir/nanite" --continue -p 'What did the fixture contain?' --output stream-json
  "$demo_dir/nanite" conversations
  "$demo_dir/nanite" rebuild-index
  "$demo_dir/nanite" conversations
  "$demo_dir/nanite" --continue
)
```

Expect the offline example to swap host tools and then report both host slots
cleared. In the live turns, check that the file result appears in the
conversation and the same conversation remains listed after rebuilding the
index. In chat, send another prompt and exit with Ctrl-D. Inspect
`$demo_dir/agent/.nanite/events.jsonl` and the conversation's JSONL segments.

To check interruption recovery, start a live turn and terminate that smoke-test
process before it completes. Restart the CLI in the same directory and accept
the resume prompt, or pass `--resume <conversation-id>`. Confirm the resumed
turn can finish and incomplete tool calls receive error results in the journal.

## Provider fixtures

`internal/provider/anthropic/testdata/replay` and
`internal/provider/openai/testdata/replay` hold real HTTP exchanges recorded
from the providers: text, a tool call, `max_tokens` during a tool call, a
prompt-cache write then read, and a closing reply sent with `tool_choice`
none. Each file keeps the request's method, API path and body and the
response's status, `Content-Type`, `Retry-After` and raw SSE bytes. Request
headers are never kept, the model alias is replaced with `recorded-model`,
and a guard refuses a recording that carries a credential-shaped string or a
hostname.

The replay tests serve these files from a local server to the real adapters,
and run whole turns through the engine with them. Failures a live provider
does not produce on demand are derived from the recordings in test code:
a stream cut mid-way, Anthropic's `overloaded_error` mid-stream, a 429 with
`Retry-After`, and `max_tokens` inside the tool arguments. A derived exchange
says so in its `source`. Replay needs no network and runs in CI.

To record again, run each package's `TestRecordFixtures` with
`NANITE_RECORD_FIXTURES=1` and a Warden identity granted `llm-gateway.chat`:
`NANITE_RECORD_WARDEN_URL`, `NANITE_RECORD_CREDENTIAL` (the parent
credential file; one process at a time, since it rotates on each mint),
`NANITE_RECORD_PRINCIPAL`, and `NANITE_RECORD_GATEWAY_URL` with
`NANITE_RECORD_MODEL` (Anthropic wire, without `/v1`) or
`NANITE_RECORD_OPENAI_URL` with `NANITE_RECORD_OPENAI_MODEL` (OpenAI wire,
with `/v1`). `NANITE_RECORD_ONLY` limits a run to named scenarios; record
`cache_write` and `cache_read` together, on a cold cache. Review the new
files before committing them.
