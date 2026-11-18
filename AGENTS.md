# nanite

A hollis-labs Go module.

It is not: TODO(author) — the mistake this repo attracts.

## Start Here

- `project.yaml` — the project descriptor (project/1, class 2): identity, interfaces, auth mode. Folio never overwrites it.
- `deploy.yaml` — fleet-install descriptor (fleet-deploy/v1).
- `nanite.cerberus.yaml` — services only: what Cerberus supervises (Option B: it does not build or place the binary).
- `cmd/nanite/substrate.yaml` — the manifest. `substrate_wiring.go` is generated from it (`substrate generate`); never edit it.
- `cmd/nanite/descriptor/` — validates project.yaml and checks that deploy.yaml, the Cerberus manifest and the code agree with it.
- `internal/authmode/authmode.go` — services only: the auth mode and the refusal of `loopback_open` on a non-loopback bind.
- `.github/workflows/check.yml` — the class 2 gate (vet, pins check, build, race tests, scan-public).

## Commands

```sh
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

CI (`.github/workflows/check.yml`) is the full gate.

## Boundaries

- project.yaml, deploy.yaml and (services) the Cerberus manifest describe one thing. Change a shared value (id, binary path, port, auth mode) in all of them; `internal/descriptor` tests fail when they differ.
- Pins are edited only in the Substrate `bom/bom.yaml`: run `substrate pins sync --app .`, never hand-edit a version in `go.mod`.
- Services: `auth: loopback_open` is for dev on a loopback bind only; the service refuses to start otherwise (`TestLoopbackOpenRefusesNonLoopback`). Cerberus-managed runs use `enforce`.
- Services: fleet-install places the binary; the Cerberus manifest has no build_strategy and no artifact_path (they would overwrite the fleet-install link).
- No secret, host name, address or personal path in a file, a commit or CI: `substrate scan-public --ref HEAD` must stay clean.
