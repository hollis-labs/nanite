# Add operator install-time capability approval gate

**Phase:** 3 — Tier 2 capability model: subprocess plugins (`TASKS/plugin-system`)
**Status:** not-started
**Depends on:** `04` (needs the declared/granted capability storage to exist)
**Touches:** `internal/plugin/install/` (`install.go`'s state machine, likely a new state or a
new `Approver`-shaped interface alongside the existing `Verifier`/`Extractor`/`Validator`/
`Loader`), `internal/api/plugins.go` (an install-flow API surface for presenting/confirming the
declared list), `cmd/nanite/plugin_cmd.go`/`plugin_install_flow.go` (the CLI's install
confirmation step), the storage `04` added.

## Context

`docs/engineering/architecture/09-plugin-system.md`: **"Install-time gate: the operator sees
the declared list and approves it (WordPress-style permission prompt), recorded alongside the
existing installed/enabled state... extends the install state machine
(`internal/plugin/install/`) and the `plugins` table rather than replacing either."**

This planning session's own research independently traced the current install state machine
to confirm there's genuinely no hook point for this today, not just an unused one:

- **`Installer.Install`** (`internal/plugin/install/install.go:144-252`) runs a fully
  automated, non-interactive pipeline: `NotInstalled → Downloading → Verifying → Extracting →
  Validating → Loading → Ready` (states at `install.go:13-22`). Every step is driven by an
  injected interface (`Verifier`/`Extractor`/`Validator`/`Loader`/`Staging`) with **no pause
  point, no callback for human sign-off, and no field for "pending permissions the operator
  must confirm."** `install.go`'s `EventFunc`/`Event` type only reports progress for UI
  display — it isn't a decision gate.
- **`verify.go`'s signature verification is supply-chain trust, not capability consent** — it
  answers "is this archive really from who it claims" (sha256 + Ed25519 signature check
  against a trusted-key lookup), a completely different question from "what is this plugin
  allowed to touch." Don't conflate the two or try to bolt capability approval onto the
  existing `Verifier` interface — it's the wrong seam.
- **`validate.go` (495 lines, read in full) has zero permission-related checks** — schema,
  cross-ref uniqueness, bundle-asset existence, platform allow-list, shadcn-version compat.
  None of it is capability-shaped. A new state/interface is needed; there's nothing to extend
  here directly, only to insert after (`Validating`, before `Loading` — the natural point,
  since `Validating` is where `04`'s parsed `Capabilities` field first becomes available, and
  `Loading` is where the plugin actually starts running).

## What to do

1. Add a new state to the install state machine — `install.go`'s state enum
   (`install.go:13-22`) — between `Validating` and `Loading` (naming suggestion:
   `PendingApproval`, adjust if a cleaner fit emerges during implementation). Add a
   corresponding `Approver` interface (mirroring the shape of the existing `Verifier`/
   `Extractor`/`Validator`/`Loader` injected dependencies) that `Installer.Install` calls at
   that point, given the manifest's declared `Capabilities` (from `04`).
2. For the CLI install/update flow (`cmd/nanite/plugin_cmd.go`, `plugin_install_flow.go`):
   print the declared capability list (MCP tools, CRUD resources, event types) and require an
   explicit confirmation (interactive prompt, or an explicit `--yes`/`--approve-capabilities`
   flag for non-interactive/scripted installs — don't silently default to full-grant in either
   mode). A plugin declaring zero capabilities skips the prompt entirely — this gate exists
   for real requests, not busywork on every install.
3. For the API-driven install/update flow (`internal/api/plugins.go`): surface the declared
   capability list in the install response/status so a caller (the plugin-manager GUI, when it
   exists — no frontend work in this task, backend-only) can present it, and require a separate
   confirming call (e.g. a new endpoint or an explicit field on the existing enable/install
   call) before capabilities move from **declared** to **granted** in `04`'s storage. Don't let
   a bare install call silently grant.
4. On approval, write the granted state into `04`'s storage (the `plugins` table's companion
   capability-grant table), scoped to exactly what was shown and confirmed — not the full
   declared set if the operator only confirmed a subset (design the confirmation payload to
   support per-item approval if that's a reasonable scope; if you judge full-set-only approval
   is sufficient for a first pass, note that judgment call explicitly in the Work Log rather
   than silently deciding it).
5. Re-declaring on a plugin update (a new version with a *different* capability list) should
   re-trigger this gate for the newly-added items, not silently carry forward the old grant set
   onto a changed request — a plugin that adds a new capability in an update must get it
   re-approved, not inherit blanket trust from its first install.

## Done means

- Installing a subprocess plugin that declares capabilities (`04`'s schema) presents them to
  the operator (CLI prompt or API-surfaced field) before the plugin reaches `Ready`/starts
  handling live traffic.
- Declining/not confirming leaves the plugin's capabilities in **declared-but-not-granted**
  state — installed, but (once `06` lands) unable to actually use any of them.
- A plugin declaring zero capabilities installs with no prompt/friction.
- Updating a plugin to a version with additional declared capabilities re-triggers approval
  for the new items specifically.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
