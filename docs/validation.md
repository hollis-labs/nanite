# Validate Nanite

Use the Go version in `go.mod`. During the rebuild, check out the companion
`libs` branch in a sibling directory. Until `agent/agentdir` is released and
pinned in `go.mod`, initialize a local workspace in their parent directory.

```sh
go work init ./nanite ./libs/agent/agentdir
cd nanite
go install ./cmd/nanite
```

An existing workspace can add the same modules with `go work use`.
The workspace is local development state. After the dependency is pinned, the
module also builds from a standalone checkout.

## Checks

The repository's check workflow runs on pull requests and pushes to `main`.
It runs vet, verifies BOM pins and generated CLI wiring, compiles packages,
runs the race suite and offline example, and scans the working tree for public hygiene. The
compile step has no artifact upload or deploy job.

```sh
go vet ./...
GOWORK=off go run github.com/hollis-labs/substrate/cmd/substrate pins check --app .
go run github.com/hollis-labs/substrate/cmd/substrate generate -f cmd/nanite/substrate.yaml
git diff --exit-code -- cmd/nanite/substrate_wiring.go
go build ./...
go test -race -count=1 ./...
go run ./examples/embed-host --offline
go run github.com/hollis-labs/substrate/cmd/substrate scan-public --tree
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

Release validation also includes lint, vulnerability scanning, coverage, and
cross-builds. Use the tool versions from the pinned Substrate BOM.

```sh
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4 run
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

Run the race suite three times with `-count=1` for release validation.
The embedding API golden test runs as part of the suite. For an intentional
API change, regenerate it with
`go test ./agent -run '^TestAPIGolden$' -update-agent-api` and review the diff.

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
