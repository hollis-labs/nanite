# [High] Plan's Track F invents new catalog infrastructure but ignores existing `internal/plugin/catalog.go` and `signature.go`

**Scope:** plan accuracy / plan-vs-reality
**Topic:** plan-accuracy
**Date:** 2026-04-10

## Problem

Nanite already has a working catalog implementation with Ed25519 signature verification at `internal/plugin/catalog.go` (270 lines) and `internal/plugin/signature.go` (96 lines), plus existing repos/source tracking in `internal/plugin/repos.go`. The plan's Track F proposes building catalog infra from scratch in a new `internal/plugin/catalog/` sub-package and a new `internal/plugin/install/` sub-package as if none of this exists. The plan is accurate about what needs to ship at the end, but wrong about the starting state — and that matters because the execution agent will either (a) build the new thing alongside the old thing and leave a dead-code graveyard, or (b) delete the old thing without knowing what it was connected to.

## Evidence

Existing catalog implementation at `internal/plugin/catalog.go:L1-40`:

```go
package plugin

import (
    "context"
    "crypto/sha256"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "sync"
    "time"

    "gopkg.in/yaml.v3"
)

// CatalogEntry represents a single plugin in a remote catalog.
type CatalogEntry struct {
    Name        string `yaml:"name"        json:"name"`
    Version     string `yaml:"version"     json:"version"`
    Description string `yaml:"description" json:"description"`
    Author      string `yaml:"author"      json:"author,omitempty"`
    Repo        string `yaml:"repo"        json:"repo,omitempty"`
    ArchiveURL  string `yaml:"archive_url" json:"archive_url"`
    Checksum    string `yaml:"checksum"    json:"checksum,omitempty"`
    Signature   string `yaml:"signature"   json:"signature,omitempty"`  // hex-encoded Ed25519
    Compat      string `yaml:"compat"      json:"compat,omitempty"`
    Runtime     string `yaml:"runtime"     json:"runtime,omitempty"`
    Tags        []string `yaml:"tags"      json:"tags,omitempty"`
```

Existing signature code at `internal/plugin/signature.go:L1-30`:

```go
package plugin

// Signature verification for catalog-distributed plugins.
//
// - Catalog entries include a "signature" field: hex-encoded Ed25519 signature
//   over the archive file bytes.
// - Each catalog source can have a trusted public key (stored in catalog_sources.public_key).
// - User-uploaded plugins (install-local, install-archive) skip verification entirely.
// - If a catalog entry has no signature, a warning is logged but install proceeds.

func GenerateKeyPair() (publicKeyHex, privateKeyHex string, err error) { ... }
```

Existing repos file at `internal/plugin/repos.go` (36 lines) handles catalog sources. `internal/plugin/manage.go` (73 lines) has `DisablePlugin`, `EnablePlugin`, `IsDisabled`, `PluginStatus` — file-based enable/disable via `plugin.yaml.disabled` rename.

The plan's Track F writes as if none of this exists:

> **F.2 — Generate the catalog root key** ... Generate an Ed25519 keypair for the catalog root ... Embed the public key in nanite's binary at a new `internal/plugin/catalog/trustedkeys.go` file

There's already `GenerateKeyPair` in `signature.go`. There's already per-source public key storage. The plan's proposed file path (`internal/plugin/catalog/trustedkeys.go`) conflicts with the existing file (`internal/plugin/catalog.go`) — you can't have both unless the old file is deleted.

Similarly, Track G creates `internal/plugin/install/install.go` as "the install state machine" — but the existing `catalog.go` has `InstallFromCatalog`, `downloadArchive`, `extractArchive`, and friends. None of these are mentioned in the plan's Track F or G.

## Impact

The plan gives an execution agent three options, all bad:

1. **Build alongside the old code.** Two catalog implementations coexist. `internal/plugin/catalog.go` and `internal/plugin/catalog/fetch.go` both exist. Grep ambiguity returns. Which one actually runs? Whoever imports which? Track I's cleanup doesn't explicitly delete the old catalog code either.

2. **Delete the old code with no impact analysis.** The execution agent reads the plan, thinks the catalog is all new, and deletes `catalog.go`. Anything currently wiring it in — the `catalog_sources` store table, the `/api/plugins/catalog` route, the CLI `plugin install` command — breaks. Track G's gate ("install from catalog works end-to-end") fails.

3. **Accurately understand what exists and rework it.** The "correct" path, but the plan provides zero information to support it. A junior execution agent will likely pick (1) or (2).

There are almost certainly other existing bits the plan is missing too. The audit at `plugin-audit-2026-04-10.md` may cover some of these but the plan says it's "superseded" and doesn't carry the inventory forward.

## Recommendation

Add a section to Track 0 (or before Track A) titled "Pre-execution inventory." List every file under `internal/plugin/` with a one-line description and its target disposition in the new plan: **keep**, **rewrite**, **delete**, or **move**. Include at minimum:

- `catalog.go` — keep (rename types? move to sub-package?)
- `signature.go` — keep
- `manage.go` — keep (enable/disable via .disabled rename is orthogonal to the new install flow)
- `repos.go` — keep
- `host.go` — keep, extensive rework per Track B
- `loader.go` — rewrite per B.4
- `config.go` — extend per B.1
- `triggers.go` — keep (plus panic recover per finding 04)
- `events.go` — keep, extend
- `filter.go`, `crud.go`, `keybindings_test.go`, `host_test.go` — keep
- `scaffold/` — rewrite per J.1 (quick patch in A.2)
- `subprocess/` — keep, extensively reworked per B+C
- `event_stream.go` — delete per plan (but acknowledge the existing code)
- `auto_triggers.go` — disposition unknown, not mentioned in plan
- `catalog_test.go`, `signature_test.go` — keep (tests should still pass after rework)

This also catches `auto_triggers.go` and `auto_triggers_test.go`, which the plan never mentions once.

Without this inventory, the plan's execution will bleed time on "wait, this file already exists" surprises. Given the plan's own opening warning about drift wasting hours, a pre-execution inventory would prevent a second class of drift.

Also recommend: a follow-up audit scope `plugin-tooling-and-tests` (see index methodology) should include a "dead-code sweep" category to find any existing implementations the plan overlooks.

## References

- `internal/plugin/catalog.go:L1-270` — existing catalog implementation
- `internal/plugin/signature.go:L1-96` — existing signature verification
- `internal/plugin/manage.go:L1-73` — existing enable/disable
- `internal/plugin/repos.go:L1-36` — existing repo/source tracking
- `internal/plugin/auto_triggers.go` — plan never mentions this file
- Plan §F.2, G.1, G.4 — all written as if catalog/signature are greenfield
