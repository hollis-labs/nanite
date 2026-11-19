# Working on Nanite

- `agent/` is the public embedding API. `internal/` holds the engine, tools,
  provider adapters, storage, and CLI-independent runtime.
- `cmd/nanite/` owns the CLI and Substrate wiring. Regenerate the wiring from
  `cmd/nanite/substrate.yaml` when changing its modules.
- Substrate imports belong under `cmd/nanite/`. Keep Substrate out of
  `agent/` and `internal/`, and keep the public `agent/` package out of
  imports from `internal/`.
- Run `go vet ./...`, `go test -race -count=1 ./...`, and
  `go run ./examples/embed-host --offline`. See [validation](docs/validation.md)
  for the full set of checks and development setup.
- When documentation and behavior disagree, trust the code and update the docs.
