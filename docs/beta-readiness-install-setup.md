# Beta Release Readiness — Install, Setup & Config Review

**Date:** 2026-05-29
**Scope:** Fresh-install → working-chat path, config/settings, distribution story.
**Out of scope (own sessions):** repo cleanup, bug fixes, polish pass, broader docs, implementation.
**Version under review:** `internal/version.Version = "0.3.0-beta"`.

## TL;DR

The engine is strong, but the **fresh-install-to-working-chat path has one hard
blocker**, and the **distribution story is essentially unbuilt** relative to the
Hollis Labs install-pattern guide. Config/settings internals are solid; onboarding
UX has fixable dead-ends. Two items gate beta; the rest are tiered below.

| Area | State |
|------|-------|
| Config/settings internals (XDG, resolver SSOT, seeding) | ✅ Solid |
| Onboarding UX (frontend) | ⚠️ Dead-ends, polish |
| **Runtime provider activation** | ❌ **Blocker** |
| **Distribution / install path** | ❌ **Not adopted** |

---

## 1. BLOCKER — A newly-added API key is not live until server restart

This is the single most important finding. The UI happy path silently dead-ends.

**Verified mechanism:**
- API keys are read **only** from the OS keychain (`internal/secrets/keyring.go`).
  There is **no env-var fallback** — `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` are
  ignored. (The demo doc's mention of `ANTHROPIC_API_KEY` is stale/incorrect.)
- The provider registry is built **once** at startup in `initProviders()`
  (`cmd/nanite/main.go:593-714`). A provider is registered **only if a key
  already exists in the keychain at boot** (`key := resolveKey(...)`; register
  iff `key != ""`). `SetAPIKey` is called once, here.
- `POST /api/providers/{id}/api-key` (`internal/api/provider_manage.go:38-61`)
  writes the key to the keychain via `secrets.Set` and **nothing else** — it does
  not re-register or reload the live registry.
- Chat dispatch resolves providers from that frozen startup registry
  (`internal/service/chat.go` `resolveProvider` → `s.providers.Get(name)`).
  The anthropic/openai wrappers cache the key on the struct (`SetAPIKey` →
  `rebuildSDK`); the keychain is never re-read per request.
- **No runtime reload path exists** — `registry.Register` is called only inside
  `initProviders`. No SIGHUP, no reload endpoint, no watch.

**User-visible consequence (fresh install, UI-only):**
1. WelcomeScreen → "Connect a provider" → Settings › Providers.
2. Enter key → "Save & Test" reports **success** (test path validates an
   ephemeral client; the key is valid).
3. Return to chat → **chat fails**. The running server never registered the
   provider. `GET provider status` confirms this split: `has_api_key: true`,
   `registered: false`.
4. Only a **server restart** re-runs `initProviders`, reads the keychain, and
   registers the provider.

For a packaged beta, "restart the daemon" is not a step a user will discover.
Positive feedback (green checkmark) followed by silent failure is the worst
shape of this bug.

**Fix candidates (for the implementation session — not decided here):**
- Reload/register the provider into the live registry when the key is set
  (cleanest; makes the UI path actually work), **or** re-resolve the key per
  request, **or** at minimum surface a "restart required" affordance.
- Add an **env-var fallback** for keys (operator/CI ergonomics, matches every
  other SDK tool users arrive from).
- Add a **CLI command** to set a key (`nanite-agent`/`nanite admin set-api-key`)
  — currently there is none; scripted/headless setup is impossible.

---

## 2. BLOCKER-ADJACENT — Distribution/install story not adopted

Measured against `~/dev/chrispian/inbox/install-pattern-guide` (Hadron/Cerberus/
Tether pattern). Nanite has adopted **none** of it yet.

- **`make install` is the anti-pattern the guide explicitly warns about.** It is
  `go install ./cmd/nanite` (Makefile:20-21) — Go-style semantics under the
  `install` name, and it installs **only the `nanite` binary**. This is a
  **multi-binary app**: `nanite` (chat daemon) **and** `nanite-agent` (framework
  injector; note `nanite install` is now a deprecation shim →
  `nanite-agent init`). A user who runs `make install` silently does not get
  `nanite-agent`. (`nanite-eval` is dev-only and stays excluded.) Guide fix:
  BSD-style `install` (PREFIX/BINDIR/DESTDIR) that installs **both** operator
  binaries; rename the old behavior to `go-install`.
- **Version/commit not stamped.** `version.GitSHA` defaults to `"dev"` and the
  doc comment shows the intended `-ldflags -X ...GitSHA=...` invocation, **but
  the Makefile wires no ldflags**. Released binaries report `0.3.0-beta (dev)` —
  ops can't map a user's binary back to a commit. (Guide footgun #1.)
- **No release pipeline.** No `scripts/release.sh`, no Homebrew formula
  generator, no goreleaser, no multi-arch (darwin/linux × amd64/arm64) tarballs,
  no `dist/checksums.txt`.
- **No `docs/install.md`.** The README "Quick Start" is a **dev** quickstart
  (`go build ./cmd/nanite/` + `serve -dev`) — no binary install, no prerequisite
  list, no API-key/provider setup, no first-run walkthrough.

Note: deps are CGO-free (`modernc.org/sqlite`), so the guide's cross-compile and
Homebrew paths are low-friction once templated.

---

## 3. Config / settings — mostly solid

- **DB & config locations** use go-apppaths/XDG; `nanite path` prints the layout
  (`cmd/nanite/path.go`). `--db` / `NANITE_DB_PATH` (+ legacy `NANITE_DB`)
  override. Config files (user `~/.config/nanite/config.yaml` + project
  `./nanite.yaml`, merged) are **optional** with safe-default fallbacks. ✅
- **Default provider/model resolution is a clean SSOT** (recent CW-0003):
  `store.ResolveProviderAndModel` walks explicit → `user_settings.default_*` →
  `providers.default_model`. Fresh seeded DB resolves to
  `anthropic` / `claude-sonnet-4-20250514`. ✅
- **Seeding is idempotent** (providers, models, templates, modes). ✅
- **Gaps:**
  - CHANGELOG (Unreleased) flags a **breaking XDG config move with no automated
    migration** — affects *upgraders*, not fresh installs, but needs a
    migration shim or a prominent doc note before release.
  - No user-facing "how to configure" doc (which knob sets what, where keys go).

---

## 4. Onboarding UX (frontend) — fixable dead-ends

Once #1 is fixed, these are the next friction points (all P2):

- **WelcomeScreen exists** and points to "Connect a provider" (`ChatMain.tsx`). ✅
- **Providers settings UI is polished** — key modal, Save & Test, status toggles
  gated on `has_api_key` (`ProviderManager.tsx`). ✅
- **Dead-ends for the no-provider user:**
  - `StartSurfaceDialog` shows "No providers available", disables Start — **no
    link to setup**.
  - Composer model picker shows **"Loading models…" indefinitely** when the list
    is genuinely empty (no "no providers configured" terminal state).
  - Send-message failure surfaces a **generic error**, not "configure a provider
    first".
  - Default model not enforced; a user can create a session with no clear
    selection.
- CLI/PTY providers expose an opaque "CLI Path" field with no example/help.

---

## 5. Plugin / optional setup (lower priority)

- **support-ticket plugin** requires Postgres `pg_trgm` (`CREATE EXTENSION`);
  fails silently without it (POST_DEMO_ISSUES.md). Document or guard at load.
- **npm version pinning** — strict-mode build broke on unpinned TS/tsconfig
  versions; pin exact versions (POST_DEMO_ISSUES.md).
- Demo docs assume Volon/Cortex/Hadron running — clarify which are optional.

---

## Recommended release-gating split

**Must fix before beta (blockers):**
1. Runtime provider activation (#1) — make the UI key-entry path actually reach
   working chat without a manual restart.
2. Minimal install path (#2 subset): `make install` ships **both** operator
   binaries; wire **ldflags** so version/commit stamp; ship a real
   `docs/install.md` + README install/setup section (binary install + first-run
   provider/key setup).

**Should fix (high-value, not strictly gating):**
3. Env-var fallback for keys **and** a CLI set-key command (headless/scripted
   setup, operator ergonomics).
4. Onboarding dead-ends (#4): link no-provider states to setup; terminal "no
   providers" state; provider-aware send error.
5. XDG config migration shim or prominent upgrade note (#3).

**Later (own sessions):**
6. Full release pipeline — `release.sh`, Homebrew formula generator, multi-arch
   tarballs + checksums (adopt the install-pattern guide end-to-end).
7. Plugin-specific setup docs (pg_trgm), npm pinning, prerequisite-services doc.

---

## Decisions locked

None — this is a review. Decisions on fix approach for #1 (live-reload vs
per-request re-resolve vs restart-prompt) deferred to the implementation session.

## Out of scope

Repo cleanup, bug fixes, polish pass, broad docs rewrite, and all implementation
— each handled in its own session per the operator's direction for this review.
