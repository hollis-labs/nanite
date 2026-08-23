# GO-SEC-003 G304 triage checkpoint — Steps 1–3

**Date:** 2026-08-22  
**Evidence snapshot:** `1d3bfd96`  
**Implementation state:** operator-approved Step 4 implemented

This was the mandatory pre-implementation checkpoint for task 08/03. Its
mechanical inventory and trust-boundary analysis remain the frozen basis for
the implementation. On 2026-08-22, the operator explicitly approved the four
coordinates and seven-file code/config footprint below; Step 4 then implemented
that selection without adding another coordinate.

## Method and count reconciliation

The list was regenerated directly from
`docs/audits/2026-08-21-go-quality/raw-1d3bfd96/golangci-baseline.json` with:

```bash
jq -r '.Issues[]
  | select(.FromLinter == "gosec")
  | select(.Text | contains("G304"))
  | select(.Pos.Filename | endswith("_test.go") | not)
  | [(.Pos.Filename | sub("^../../../"; "")), (.Pos.Line | tostring), .Text]
  | @tsv' \
  docs/audits/2026-08-21-go-quality/raw-1d3bfd96/golangci-baseline.json \
  | sort -k1,1 -k2,2n
```

This produces exactly **70 production G304 sites**, agreeing with
`raw-1d3bfd96/DELTA.md` (audit 68 → frozen-HEAD 70).

| Reconciliation | Sites |
|---|---:|
| Mechanical production G304 total | 70 |
| Excluded: four previously-triaged `cmd/nanite` files | 6 |
| Excluded: `internal/agent/managed_files.go` + `managed_section.go` | 5 |
| Remaining classified inventory | **59** |

Coordinates below are the immutable coordinates recorded in the frozen raw
JSON. Current `HEAD` has since moved some lines and Wave 1 retired the old
catalog verification helpers behind three coordinates
(`internal/api/catalog.go:466`, `internal/plugin/catalog.go:257`, and
`internal/plugin/signature.go:86`). They remain in the 70-site source list so
the checkpoint is mechanically reproducible; their current reachability is
recorded below.

## Required exclusions

| Frozen site(s) | Count | Disposition |
|---|---:|---|
| `cmd/nanite/mcp_cmd.go:32` | 1 | Previously traced in report §8.13; operator CLI-controlled, documentation-only. |
| `cmd/nanite/plugin_cmd.go:482,491` | 2 | Previously traced in report §8.13; operator CLI-controlled, documentation-only. |
| `cmd/nanite/plugin_logs.go:22` | 1 | Previously traced in report §8.13; operator CLI-controlled, documentation-only. |
| `cmd/nanite/serve_autostart.go:82,202` | 2 | Previously traced in report §8.13; the report found CLI or deterministic OS state, and the task assigns the operator-CLI disposition; documentation-only. |
| `internal/agent/managed_files.go:151,180` | 2 | Excluded as the `internal/agent/managed_*` subset owned by the already-reviewed 03/01 task. Not re-analyzed here. |
| `internal/agent/managed_section.go:33,87,131` | 3 | Mandated `internal/agent/managed_*` exclusion, but **not** owned or fixed by 03/01. These coordinates were previously audit-traced: lines 33 and 131 are live accepted `install_project` / operator-authored-root capability paths, while line 87 has no production caller. |

## Step 2 — complete classification of the 59 remaining sites

The category is based on the least-trusted live production origin reaching a
sink. An HTTP route is classified `external unauthenticated` because
`basicAuthMiddleware` is a no-op when the two auth environment variables are
unset and the default listener binds all interfaces (the direct verification
already recorded in reviewed task 03/01). This is the current pre-08/07 API
trust classification.

| Frozen site(s) | Count | Required taxonomy | Construction / disposition |
|---|---:|---|---|
| `internal/agent/parser.go:175` | 1 | OS-derived/internal | Managed/discovered `SourceRef`; current managed paths are produced by reviewed task 03/01's validated/constrained path layer. The dormant CLI-file tier has no production caller. |
| `internal/agentworkflow/definition_yaml.go:48` | 1 | operator CLI-controlled | File comes from the operator-configured workflow-definition directory; directory entries come from `os.ReadDir`. |
| `internal/api/artifacts.go:113,205` | 2 | external unauthenticated | Artifact ID / upload fields enter through HTTP. Both sinks are already preceded by `pathsafe.ResolveUnder`; upload filename and session are also single-segment validated. |
| `internal/api/catalog.go:466` | 1 | OS-derived/internal | Frozen `checksumFile` helper had no production caller and remains callerless after the catalog install convergence; no live sink to fix. |
| `internal/api/plugins.go:969,980,998,1054,1130` | 5 | external unauthenticated | `install-local` accepts an HTTP `path`; `install-archive` accepts multipart archive bytes. Archive destinations already use `pathsafe.ResolveUnder`. The accepted local-source root contract is arbitrary absolute path, but `copyFile` at line 969 follows source symlinks; line 980 opens the already-confined destination and is not selected on current evidence. |
| `internal/assets/framework.go:85` | 1 | operator CLI-controlled | `nanite-agent init` target (or default home directory) → `InstallHome` → `assets.ExtractTo`; embedded relative names are internal. |
| `internal/config/appconfig.go:154` | 1 | OS-derived/internal | Callers pass the fixed repo-relative `config/nanite.yaml` path. |
| `internal/config/config.go:206` | 1 | OS-derived/internal | XDG/home-derived user config and CWD-relative `nanite.yaml`. |
| `internal/contextbroker/source_pcc.go:99` | 1 | external unauthenticated | `session.project_id` is HTTP-settable and becomes `Intent.Scope`; `resolveProjectDir` currently joins it beneath `.nanite/pcc/global` without confinement. |
| `internal/mcp/dev_tools.go:654,779,949` | 3 | agent-controlled | Model tool arguments reach `dev_read`, `dev_grep`, and `dev_edit`; each top-level path passes `resolveAllowed`. The unresolved per-entry grep symlink gap at line 779 is separately tasked to 08/02, not accepted as safe here. |
| `internal/permission/rules.go:158` | 1 | OS-derived/internal | Exported loader has no production caller at either the frozen commit or current `HEAD`. |
| `internal/plugin/agent_profiles.go:164` | 1 | external unauthenticated | `install-local` / `install-archive` → hot-load → `ApplyManifestRegistrations` → `resolvePluginAssetPath` → parser/open. The API route is least-trusted; archive extraction does not preserve links as installed-tree symlinks, while local copy dereferences a source link earlier at `plugins.go:969` and materializes a regular installed file. |
| `internal/plugin/builtin/adapter-nanite-native/plugin.go:266` | 1 | operator CLI-controlled | `agents[].context` from project `.nanite/config.yaml` is joined under that project config directory; operator-authored Native config paths are accepted exceptions under the task invariant. |
| `internal/plugin/builtin/adapter-nanite-native/plugin.go:385` | 1 | OS-derived/internal | Global config location is derived from the user's home directory. |
| `internal/plugin/builtin/adapter-nanite-native/plugin.go:401,423` | 2 | operator CLI-controlled | Project config path and global role `file:` entries are operator-authored config and accepted exceptions under the task invariant. |
| `internal/plugin/catalog.go:233,257` | 2 | plugin-catalog-controlled | Catalog source → SHA-derived disk-cache path; frozen `VerifyChecksum` read an internally-created download temp file. The latter helper is retired on current `HEAD`. |
| `internal/plugin/catalog/fetch.go:137,226,230` | 3 | plugin-catalog-controlled | Catalog URL → SHA-256 cache basename under configured cache root; YAML/signature cache paths cannot contain URL text directly. |
| `internal/plugin/config.go:364,380` | 2 | external unauthenticated | `install-local` / `install-archive` → hot-load → `NewPluginConfig` reads fixed `plugin.yaml` / `config.yaml` leaves under the newly installed root. The API installation paths are the least-trusted live origin. |
| `internal/plugin/config.go:392` | 1 | external unauthenticated | Shared manifest parser is reachable from `POST /api/plugins/install-local`, whose `req.Path` selects the source directory; it also has safer installed-root and CLI callers. |
| `internal/plugin/envelope_validator.go:81` | 1 | external unauthenticated | `install-local` / `install-archive` → hot-load manifest registration → lexical `resolvePluginAssetPath` → schema read. Archive extraction does not preserve links as installed-tree symlinks; local install has already dereferenced any source link at `plugins.go:969`. |
| `internal/plugin/install/download.go:83` | 1 | plugin-catalog-controlled | Catalog archive URL basename is reduced with `path.Base`, then written in an internally-created staging directory with `O_EXCL`. |
| `internal/plugin/install/extract.go:176` | 1 | plugin-catalog-controlled | Archive header name is absolute/`..` checked; links are rejected; destination is within the fresh staging root. |
| `internal/plugin/install/extract.go:279,284` | 2 | operator CLI-controlled | Directory-source install comes from an explicit local CLI source; `Walk` + `Lstat` reject symlinks before the copy helper. |
| `internal/plugin/install/staging.go:46` | 1 | plugin-catalog-controlled | Catalog/plugin ID → `ValidatePluginID` allowlist → lock beneath configured staging root. |
| `internal/plugin/install/validate.go:125` | 1 | plugin-catalog-controlled | Installer supplies the fixed staged `plugin.yaml` path; manifest content is catalog-controlled but the path is internal. |
| `internal/plugin/repos.go:25` | 1 | OS-derived/internal | API plugin manager reads its fixed configured `repos.yaml`; request selects a name, not this path. |
| `internal/plugin/scaffold/scaffold.go:207` | 1 | operator CLI-controlled | `nanite plugin new` destination plus embedded template-tree relative path. |
| `internal/plugin/signature.go:86` | 1 | plugin-catalog-controlled | Frozen catalog verification read an internally-created downloaded archive. Verification helpers have no current production caller after Wave 1 convergence. |
| `internal/service/durable_agent_recipes.go:1118` | 1 | operator CLI-controlled | Recipe catalog paths originate in operator application configuration; directory entries are OS-derived. |
| `internal/service/install/adapter_persist.go:22,47` | 2 | agent-controlled | `nanite_install_project(project_dir)` → `InstallProject` → fixed `.nanite/config.yaml` read/persist path. CLI is a second, higher-trust caller. |
| `internal/service/install/adapters.go:81,119,161,192` | 4 | agent-controlled | Same self-tool root → fixed config and adapter-target leaf names used during sync/snapshot. |
| `internal/service/install/install.go:180` | 1 | operator CLI-controlled | `InstallHome` target/default home → internally-created sibling staging tree; this `copyTree` site is not on the agent-controlled `InstallProject` branch. |
| `internal/service/managed_durable_configs.go:339` | 1 | OS-derived/internal | Configured managed/global durable-agent directories → `os.ReadDir` entries → read discovered YAML. |
| `internal/service/slot_stash.go:345` | 1 | OS-derived/internal | Configured stash root plus deterministic content-addressed artifact ID; content does not influence the path. |
| `internal/skill/parser.go:122` | 1 | OS-derived/internal | Legacy single-file skill parser has no production caller on the frozen commit or current `HEAD`. |
| `internal/skill/parser.go:177,218` | 2 | external unauthenticated | `POST /api/skills/install {path}` → `ParsePackageDir(req.Path)` → `SKILL.md` and package-tree reads. CLI install is a second caller. File symlinks are currently followed. |
| `internal/skillvendor/address.go:138` | 1 | OS-derived/internal | Validated content address → configured vendor root; vendored writes materialize regular files from an in-memory map. |
| `internal/tool/yaml_loader.go:93` | 1 | OS-derived/internal | Loader currently has no production caller; its defined roots are project/user `.nanite/tools` directories. |
| `internal/toolclient/skills.go:158` | 1 | operator CLI-controlled | Operator-configured `SkillsDir` (or home-derived default) → first-level directory walk. |
| `internal/workflow/loader.go:55` | 1 | OS-derived/internal | Exported loader has no production caller at either the frozen commit or current `HEAD`. |
| `internal/workspace/walkup.go:265` | 1 | external unauthenticated | HTTP-settable `projects.repo_path` → session project lookup → `WorkspaceCache.Refresh` → `WalkUp` → allowlisted instruction-file read. The unresolved repo-root policy is separately tasked to 08/09/AD-27, not accepted as safe here. |

### Classification totals

| Taxonomy | Sites |
|---|---:|
| external unauthenticated | 16 |
| external authenticated | 0 |
| agent-controlled | 9 |
| plugin-catalog-controlled | 10 |
| operator CLI-controlled | 11 |
| OS-derived/internal | 13 |
| **Total** | **59** |

## Step 3 — origin traces for all 35 sites above operator/OS trust

Each row covers every site in the three higher-risk classes; counts reconcile
to 16 + 9 + 10 = **35**.

| Origin / call chain | Sites | Count | Existing confinement and result |
|---|---|---:|---|
| HTTP artifact download/upload → `handleDownloadArtifact` / `handleUploadArtifact` → G304 sink | `internal/api/artifacts.go:113,205` | 2 | Both already use `pathsafe.ResolveUnder`; no Step-4 change proposed. |
| HTTP local/archive plugin install → `handleInstallLocal` / `handleInstallArchive` → `copyDir`/`copyFile` or extract helper | `internal/api/plugins.go:969,980,998,1054,1130` | 5 | Archive entries are confined and bounded, and extraction does not preserve link entries as installed-tree symlinks. Local install intentionally accepts an arbitrary absolute server-local root. `copyDir` follows a source file symlink at line 969 before writing regular bytes to the confined destination; this source dereference is selected. Line 980 only opens that confined destination and is not selected on current evidence. |
| HTTP create project/session (`project.id` / `session.project_id`) → `ContextClient.deriveIntent` (`Intent.Scope`) → `PCCSource.Fetch` → `resolveProjectDir` → read | `internal/contextbroker/source_pcc.go:99` | 1 | Raw `filepath.Join(BasePath, scope)` can escape. Proposed Step-4 selection. |
| HTTP local plugin install `req.Path` → `ParseManifest(filepath.Join(abs(req.Path), "plugin.yaml"))` | `internal/plugin/config.go:392` | 1 | The ambient arbitrary-absolute-root contract is explicit; this is a fixed leaf under the caller-selected root. No sink-local `ResolveUnder` root exists beyond that accepted operation root. |
| HTTP skill install `req.Path` → `skillinstall.Installer.Install` → `ParsePackageDir` → `SKILL.md` / `WalkDir` reads | `internal/skill/parser.go:177,218` | 2 | Root is an intentional local package path, but symlinked files can escape it. Proposed Step-4 selection confines/rejects per-entry symlinks. |
| HTTP project `repo_path` → session project → `WorkingDirForSession` → `WorkspaceCache.Refresh` → `WalkUp` → allowlisted read | `internal/workspace/walkup.go:265` | 1 | Leaf and intermediate symlinks are rejected, but the repo-root policy remains unresolved and is separately tasked to 08/09/AD-27; no duplicate change here. |
| Agent tool call → `DevToolsTransport.CallTool` → `callRead` / `callGrep` / `callEdit` | `internal/mcp/dev_tools.go:654,779,949` | 3 | Top-level paths pass `resolveAllowed`/`pathsafe`. The `callGrep` per-entry symlink gap at line 779 remains unresolved and is separately tasked to 08/02; no duplicate change here. |
| Agent self-tool `install_project(project_dir)` → `SelfToolsTransport.callInstallProject` → `Service.InstallProject` → config read/persist and adapter snapshots | `internal/service/install/adapter_persist.go:22,47`; `internal/service/install/adapters.go:81,119,161,192` | 6 | `project_dir` is intentionally an ambient arbitrary-absolute-root capability and becomes the operation root after `Abs`/`EvalSymlinks`; fixed leaves stay beneath it. No new grants architecture is proposed. |
| HTTP local/archive install → copy/extract → `runPluginLoadIntoHost` → `NewPluginConfig` / `ApplyManifestRegistrations` → installed config/profile/schema reads | `internal/plugin/config.go:364,380`; `internal/plugin/agent_profiles.go:164`; `internal/plugin/envelope_validator.go:81` | 4 | These are `external unauthenticated` by their hot-load origin. Archive extraction does not preserve link entries as installed-tree symlinks. Local copy follows a hostile source link earlier at `plugins.go:969` and writes a regular installed file, so strengthening the later shared asset helper cannot repair that earlier dereference. A helper hardening is optional defense-in-depth absent evidence of another supported installed-tree origin that preserves hostile symlinks. |
| API/catalog browse/install → configured catalog source → `CatalogFetcher.Fetch`; or signed CLI catalog → `SignedFetcher.Fetch` → SHA-derived cache | `internal/plugin/catalog.go:233`; `internal/plugin/catalog/fetch.go:137,226,230` | 4 | Cache basenames are hashes, not source text. Safe by construction; no change. |
| Frozen catalog install → downloaded temp archive → `VerifyChecksum` / `VerifySignature` | `internal/plugin/catalog.go:257`; `internal/plugin/signature.go:86` | 2 | Both production paths were retired by Wave 1 convergence. No live sink. |
| Catalog/manifest archive URL → `HTTPDownloader.Download` → safe URL basename under staging | `internal/plugin/install/download.go:83` | 1 | `path.Base` + internal staging root + `O_EXCL`; no change. |
| Catalog archive header → `TarGzExtractor.extractArchive` → validated `destPath` | `internal/plugin/install/extract.go:176` | 1 | Rejects absolute paths, `..`, symlinks/hardlinks, devices, and size/ratio excess; no change. |
| Catalog/plugin ID → installer → `DirStaging.Begin` lock path | `internal/plugin/install/staging.go:46` | 1 | `ValidatePluginID` precedes join; no change. |
| Catalog archive staged root → `ValidateManifest` → fixed manifest path | `internal/plugin/install/validate.go:125` | 1 | Path internally constructed by installer; no change. |

## Approved and implemented Step-4 selection

The operator approved **4 frozen G304 coordinates across three boundaries and
three production files**:

1. `internal/contextbroker/source_pcc.go:99`: replace raw scope/entry joins with
   `pathsafe.ResolveUnder` so a crafted project ID cannot escape the PCC root.
2. `internal/skill/parser.go:177,218`: resolve `SKILL.md` and every walked file
   beneath the selected package root with `pathsafe.ResolveUnder` (or reject
   symlinks explicitly before reading), preventing a package from smuggling
   files from outside its root into the vendor store.
3. `internal/api/plugins.go:969`: reject source-tree symlinks before
   `copyFile` opens them during `install-local`. The accepted caller-selected
   root does not make a symlink to a file outside that root safe. Do not select
   line 980: it opens the already-confined destination. Archive extraction
   does not preserve link entries as installed-tree symlinks.

`internal/plugin/agent_profiles.go:164` and
`internal/plugin/envelope_validator.go:81` are optional defense-in-depth, not
confirmed mandatory vulnerabilities. No supported installed-tree origin that
preserves a hostile symlink was established: archive extraction does not
preserve link entries as symlinks, and local installation dereferences a source link at `plugins.go:969` and
materializes a regular destination file. Hardening `resolvePluginAssetPath`
cannot repair that earlier read.

Implemented code/config footprint:

- `internal/contextbroker/source_pcc.go`
- `internal/contextbroker/source_pcc_test.go`
- `internal/skill/parser.go`
- `internal/skill/parser_test.go`
- `internal/api/plugins.go`
- `internal/api/plugins_install_test.go`
- `.golangci.yml` — extend the existing `ResolveUnder` / `filepath.Join`
  forbidigo scope to the exact `source_pcc.go` and `skill/parser.go` files
  after their remaining raw joins are converted. Do not add `plugins.go` to
  that rule: its selected fix is source-symlink rejection, the rule cannot
  express `os.Open` symlink-follow policy, and the file contains numerous
  unrelated joins.

The Step-4 tests cover dotdot-mid-path, symlink escape, and
symlink-escape-subpath for the PCC and skill-package confinement boundaries,
plus an install-local regression proving a source-tree file symlink is rejected
before its external target is read or copied. Expected code touches are limited
to the seven files above. The unresolved `dev_tools.go:779` walk remains with
08/02, and the unresolved `workspace/walkup.go:265` root policy remains with
08/09/AD-27.

## Step-4 authorization and result

The operator explicitly approved the evidence-backed four-coordinate,
three-boundary selection and seven-file code/config footprint. Implementation
is complete at those coordinates, and post-fix `gosec -include=G304 ./...`
reports no finding at any selected sink. This records operator authorization
only; no review or orchestrator approval is claimed by this checkpoint.
