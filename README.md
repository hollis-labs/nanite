# nanite

A hollis-labs class 2 Substrate CLI tool.

Class 2 tool (project/1 class model, RC1): `project.yaml` is the descriptor, `deploy.yaml` is read by `fleet-install`.

## Build

```sh
go mod tidy
go run github.com/hollis-labs/substrate/cmd/substrate pins sync --app .
go mod tidy
go build -o nanite ./cmd/nanite
./nanite hello
```

## Deploy

`fleet-install deploy nanite --commit <sha> --dry-run`, then without `--dry-run`. Set the host alias in `deploy.yaml` first.

## Installed configuration

The binary reads `$XDG_CONFIG_HOME/nanite/config.yaml` when present (fallback
`~/.config/nanite/config.yaml`). `SUBSTRATE_CONFIG` explicitly selects another file.
Config discovery creates nothing and does not read from the checkout. Store credentials in an
operator-owned environment file or secret provider outside the repository.



## Regenerate deployment descriptors

Edit `project.yaml` → `settings.runtime`, then run `folio deploy render . --plan` and review the result.
`folio deploy render . --write` updates generated descriptors and their tracking digests. It refuses
local edits to those outputs unless you explicitly add `--overwrite` after review. It never rewrites
`project.yaml`, starts a service or installs a binary. `env_file`, if set, references operator-owned
configuration outside the repository; no credentials belong in the descriptor. Changing runtime
target OS/architecture also requires updating CI build targets. Application auth/code are not rewritten.
