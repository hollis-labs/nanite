# CI-built binaries installed from GitHub (CW-20261024-0120): design proposal

Status: PROPOSAL, nothing built. Owner direction (2026-10-24): binaries are built once in CI and installed from GitHub; nothing is built on a target machine; local repo copies are going away. Constraint: no tag is created without the owner's approval; releases (v0.4.0 and so on) stay separate. Cerberus (Local Dev) is a participant; its contract is `ci-build-deploy-workflow-v1`, and its drafts for basalisk, station, mentat and fragments-engine are in `~/dev/workspaces/sysadmin/docs/phaseB-drafts-20261024.md`. This design aims to be one contract for all of them.

## 1. CI workflow

New `.github/workflows/build-binaries.yml` in this repo (additive; `release.yml` stays the tag-only macOS/Homebrew path).

- **Trigger:** `workflow_run` of "Go test" on `main`, `conclusion == success`, plus `workflow_dispatch` (input: ref). A binary exists only for a commit whose full suite passed. (A plain `push` trigger would race the tests; the deploy command also re-checks the commit's check runs.)
- **Runner:** `ubuntu-latest` (hosted). One job cross-builds both targets, so the two binaries come from the same checkout and toolchain. The self-hosted `build` runner group from the Cerberus draft is not needed here and is the riskier choice on a public repo.
- **Steps:** checkout with `fetch-depth: 0` (tags for `git describe --match 'v*'`), `setup-go` from `go.mod`, `setup-node` (22), `make generate-envelopes build-ui` (the UI is embedded), then for each of `linux/amd64` and `darwin/arm64`: `CGO_ENABLED=0 GOOS=.. GOARCH=.. GOWORK=off go build -trimpath -ldflags "-s -w -X ...version.GitSHA=<sha12> -X ...version.Version=<describe>"` (the same ldflags `make build` uses).
- **CGO-free check:** Nanite already uses `modernc.org/sqlite` and ships `CGO_ENABLED=0` darwin archives and a `CGO_ENABLED=0` Dockerfile build, so cross-compiling from Ubuntu is expected to work. The workflow proves it instead of assuming it: the build itself runs with `CGO_ENABLED=0`; a step fails if `go list -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./...` prints anything outside the allowlist; `go version -m` of each binary is checked for `CGO_ENABLED=0`; the linux binary is run once (`nanite version`) and started against a temporary data dir to read `/api/health`.
- **Outputs per commit:** `nanite_<version>_<os>_<arch>.tar.gz` (a bare `nanite` binary, `deploy.yaml`, README, LICENSE), `SHA256SUMS`, and `build-info.json` (`commit`, `version`, `go_version`, `migrations_max` read from `internal/store/migrations`, so a deploy can tell whether a migration is coming). Uploaded as one workflow artifact named `nanite-<sha12>` (same naming as the Cerberus draft). Optional but recommended: `actions/attest-build-provenance` so the installer can run `gh attestation verify`.
- **Reuse:** if Cerberus' reusable workflow (`hollis-labs/tools/.github/workflows/go-app-build.yml`) accepts a pre-build command, Nanite calls it with `make generate-envelopes build-ui`; if not, this workflow produces the identical contract (names, sums, artifact name). The proposal is that the contract is the shared thing.

## 2. Artifact or rolling prerelease

Recommendation: **workflow artifacts, no tags.**

| | Workflow artifact (recommended) | Rolling prerelease (`ci-main`) |
| --- | --- | --- |
| Tag needed | none | one moving non-version tag, needs the owner's approval; force-moved on every build |
| Download auth | any GitHub token, even for a public repo (the artifact API needs one) | none (public repo release assets) |
| History | one artifact per commit, default 90-day retention (can be raised) | only the latest, unless per-commit prereleases are created (tag churn) |
| Deploy "any commit" | yes, by sha | no |

If the owner would rather have zero-credential downloads, the rolling prerelease is the alternative; it still needs a decision on the tag. Artifacts expire; a deploy of an old commit may need a rebuild via `workflow_dispatch`.

## 3. Credentials to download artifacts

Options, in order of preference:
1. **Reuse the existing login.** agent-os already has `gh` authenticated (account `chrispian`, keyring) and so does the Mac. Zero new credentials, but the token is the owner's broad one.
2. **A fine-grained read-only token per machine** (Actions: read, Contents: read on `hollis-labs/nanite` and the other CI repos), stored in a 0600 file, passed by `GH_TOKEN_FILE`. The owner issues it (this agent issues no credentials).
3. **Pull from a machine that has a token** and copy the tarball over (`fleet-install --from-file`), so targets hold nothing.
4. **Rolling prerelease** (no token), see section 2.

Question for the owner: option 1 now, option 2 when more machines are enrolled?

## 4. The install command: `fleet-install`

One command, generic (not Nanite-specific), driven by a `deploy.yaml` that ships inside the tarball so the configuration always matches the version being installed. It is a single static Go binary (no Python or YAML library on the target), built by the same CI contract and living in `hollis-labs/tools`; it is installed by hand once, then updates itself. (A bash version is possible for Nanite alone but cannot be tested or shared as well.)

```
fleet-install deploy  <app> [--commit SHA | --run ID | --latest-green] [--target ID] [--dry-run] [--accept-migration] [--force]
fleet-install deploy  <app> --from-file nanite_<v>_<os>_<arch>.tar.gz   # no GitHub access needed
fleet-install rollback <app> [--target ID]
fleet-install status   <app>          # installed version, previous version, health
fleet-install list
```

Flow, each step logged and fatal on failure:
1. **Resolve** the commit to a green run (`gh run list --commit`; refuses a commit whose required checks are not green unless `--force`), download the artifact, **verify** `SHA256SUMS` (and the attestation when present).
2. **Read** `deploy.yaml`; pick the target for this host and the matching `linux/amd64` or `darwin/arm64` tarball.
3. **Preflight:** active-work check (a SQL probe, for Nanite `agent_runtime` in `launching/running`, as `scripts/mac-nanite/predeploy.sh` does); `sqlite3 <db> ".backup <file>"` per database with `pragma integrity_check` (works on WAL databases; mentat's 739 MB takes about 0.7 s); optional data tarball; compare `build-info.json` `migrations_max` with the database's goose version and stop unless `--accept-migration` (the Architect is mailed before any deploy that carries one).
4. **Install:** extract to `<install_root>/<version>/<binary>`, mode 0755; record the previous version; flip each `symlink` atomically (temp link plus rename); write each `unit_dropin` idempotently (for station's two Cerberus-rendered units one tarball feeds two symlinks through an `ExecStart=` reset drop-in); keep the last three versions.
5. **Restart** through the configured restarter (`cerberus`, `systemd`, or `print` to emit the request for a human click, see section 6), in the listed order.
6. **Health check** with an expected-status list (mentat answers 401 on `/api/repos`, which proves liveness) and, when the endpoint reports one, the expected version, within a timeout.
7. **Roll back** on failure: relink the previous version, restart, re-check; print the database backup path and the restore commands. A restore is manual because a migration that already ran is not safely reversible; `rollback` can also be run by hand later.

## 5. `deploy.yaml` schema (v1), with Nanite as the example

```yaml
schema: fleet-deploy/v1
app: nanite
repo: hollis-labs/nanite
artifact: {name: "nanite-{sha12}", tarball: "nanite_{version}_{os}_{arch}.tar.gz", sums: SHA256SUMS}
targets:
  - id: agent-os-hub-nanite
    when: {os: linux, arch: amd64}              # which tarball; host match is by --target or hostname list
    hosts: [agent-os]
    binary: nanite                               # file inside the tarball
    install_root: /var/lib/fabric/releases/nanite
    symlinks:
      - path: /var/lib/fabric/bin/nanite         # the path the unit runs today, replaced by a symlink
    unit_dropins: []                             # [{unit, name, content}], idempotent
    preflight:
      - {kind: sql_zero, db: /var/lib/fabric/xdg/data/nanite/workspaces/default/main.db,
         sql: "select count(*) from agent_runtime where state in ('launching','running')"}
      - {kind: sqlite_backup, db: /var/lib/fabric/xdg/data/nanite/auth/auth.db,  dest: "{backup_dir}/auth.pre-{version}.db"}
      - {kind: sqlite_backup, db: /var/lib/fabric/xdg/data/nanite/workspaces/default/main.db, dest: "{backup_dir}/main.pre-{version}.db"}
      - {kind: migration_check, db: /var/lib/fabric/xdg/data/nanite/workspaces/default/main.db}
    restart: {via: systemd-user, unit: com.hollis-labs.cerberus.hub-nanite.service}   # or {via: cerberus, resource: hub-nanite, approval: print}
    health: {url: "http://127.0.0.1:8090/api/health", expect_status: [200], timeout: 60s, version_json: version}
  - id: mac-nanite
    when: {os: darwin, arch: arm64}
    hosts: [mac]
    binary: nanite
    install_root: ~/.local/share/nanite/releases
    symlinks: [{path: ~/.local/share/nanite/current/nanite}]
    preflight: [ ...same shape with the mesh-chat databases... ]
    restart: {via: cerberus, resource: nanite-api-service, verb: apply}   # local user, --ack
    health: {url: "http://127.0.0.1:8090/api/health", expect_status: [200], timeout: 60s, version_json: version}
```

How the other apps' needs map (from the Cerberus doc, section 3): per-DB `sqlite_backup` entries (WAL ok); `health.expect_status: [401]` for mentat; station's two units are two `symlinks` entries plus one `unit_dropins` entry that resets `ExecStart=` and two `restart` steps (usage monitor first, ordered); fragments-engine and basalisk are one symlink each. Restarts of agent-os resources that need a Cerberus write use `via: cerberus, approval: print`, which prints the exact request for the Approve and Run click instead of running it.

## 6. Cerberus changes

- **Mac `nanite-api-service`:** stop building from the checkout. Remove `build_strategy` and point `command[0]` at the stable downloaded binary (`~/.local/share/nanite/current/nanite`); keep `run_from: artifact`. `fleet-install` flips the symlink and runs `cerberus resource apply nanite-api-service --ack` (Cerberus copies `command[0]` into its install dir and activates it). This is a change to the repo's `nanite.cerberus.yaml`, no privileged action.
- **agent-os `hub-nanite`:** smallest change first: no Cerberus edit. `/var/lib/fabric/bin/nanite` becomes a symlink managed by `fleet-install`; the unit and the `run_from: workspace` manifest stay as they are. Restart stays `systemctl --user restart` (the manual restart the Architect already approved) or becomes `cerberus resource reload hub-nanite` with an Approve and Run click; the owner chooses. Moving `hub-nanite` to `run_from: artifact` is possible later; it is a privileged manifest change (Approve and Run).
- **Cerberus (Local Dev):** one question: can `resource apply` take the artifact source from a symlink target outside `~/.cerberus` (the `command[0]` path)? It does today for `./nanite`, so yes.

## 7. Plan, tests, docs

1. **P1** workflow PR (no deploy): prove two binaries per green main commit; CGO and version checks; artifact name and sums as above.
2. **P2** `fleet-install` plus Nanite's `deploy.yaml` (PR in `tools` and here); tested with `--dry-run`, then on the Mac instance (no sessions): deploy N, deploy N+1, forced health failure to exercise the automatic rollback, `rollback` by hand.
3. **P3** Mac Cerberus resource switched to the downloaded binary; `docs/guides/mac-nanite-supervision.md` and the deploy docs updated; `scripts/mac-nanite/predeploy.sh` retired into the `preflight` list.
4. **P4** agent-os: only after the owner lifts the hold on agent-os deploys; first deploy by hand-supervised `fleet-install` with the owner's click for the restart.
Done when a green main commit yields both binaries, and any commit can be deployed to agent-os and the Mac with one command, no local build or checkout, and rollback has been tested.

## 8. Decisions needed from the owner

1. Artifacts (no tags) or a rolling `ci-main` prerelease (one moving tag)?
2. Download credential: reuse the existing `gh` login, or a per-machine read-only token?
3. `fleet-install` as a Go binary in `hollis-labs/tools` (recommended) or a bash script in this repo?
4. agent-os restart path: keep `systemctl --user` (approved manual exception) or Cerberus with Approve and Run?
5. Is `hub-nanite` to stay `run_from: workspace` behind a symlink for now?
6. Attestation (`gh attestation verify`) required or optional?

## 9. Not verified

No workflow has been run, no `fleet-install` exists, and the Cerberus apply-from-symlink behaviour is read from code and docs, not tested. `migrations_max` extraction and `actions/attest-build-provenance` availability for this repo are assumptions to check in P1.
