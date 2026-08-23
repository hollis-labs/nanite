# Remove the default seeded "official" catalog source

**Phase:** Audit remediation — Wave 9 (follow-ups)
**Status:** not-started
**Depends on:** `01/01` (landed) — this only makes sense after the fail-closed convergence, which is what turned an unkeyed source from "works insecurely" into "always rejects."
**Blocks:** nothing.
**Parallel-safe with:** everything in this wave and every other open task — it touches the seed path only.
**Touches:** `internal/store/seed.go` (the `catalog_sources` INSERT at `:176-184`), its test, and whatever surfaces the catalog in the UI (read-only for this task — see Non-goals).
**Gated on:** AD-05 — **decided 2026-08-22: remove it.**
**requires_security_review:** false
**requires_regression_test:** true

## Context

### Decision

**AD-05 (2026-08-22): remove the default seeded catalog source. Do not
provision a signing key.**

### Why

`internal/store/seed.go:176-184` seeds one row:

```go
INSERT OR IGNORE INTO catalog_sources (id, name, url, type, priority)
VALUES ("official", "Hollis Labs",
        "https://raw.githubusercontent.com/hollis-labs/plugin-catalog/main/catalog.yaml",
        "official", 100)
```

It sets no `public_key`. Before Wave 1 that didn't matter much, because
`handleCatalogInstall` skipped signature verification when no key was
configured — silently, which is exactly what made `GO-PLUGIN-001` critical.

`01/01` fixed that: installs now **fail closed**. Which means the seeded source
is no longer insecure — it is **inert**. Every install attempt from it is
rejected, out of the box, on a fresh database.

A shipped default that always fails is worse than no default. It teaches an
operator that the feature is broken rather than that it needs configuring, and
it does so on first contact.

Provisioning a real key was considered and rejected. It is genuine external
work with no owner: keypair generation, secure custody of the private key, a
rotation and revocation policy, and a signing step in whatever publishes
`catalog.yaml`. None of that exists, and inventing it to justify a seeded row
is backwards.

### Desired invariant

**A fresh database has no catalog source, and the product says so plainly
rather than appearing broken.** Operators add and key their own sources
deliberately.

## What to do

1. **Remove the INSERT** at `internal/store/seed.go:176-184`, along with the
   surrounding `// --- Catalog sources ---` block if nothing else lives in it.

2. **Decide and record what happens to existing databases.** `INSERT OR
   IGNORE` means every database seeded before this change still carries the
   `official` row. Options, and this task must pick one explicitly rather than
   leave it implied:
   - **Leave existing rows** — simplest, and consistent with "seed only
     affects fresh databases." Existing installs keep an inert source that
     fails on use.
   - **Remove it via migration** — cleaner end state, but it deletes a row an
     operator may have since added a key to. If you take this, the migration
     must delete only rows still matching the seeded shape *and* having no
     `public_key`.

   The second is safer than it sounds and probably right, but it needs a
   migration number — re-list `internal/store/migrations/` before claiming one.
   **This batch has otherwise claimed no migrations**; if you add one, say so
   in the batch README so the next reader isn't surprised.

3. **Confirm the browse path degrades honestly.** `handleBrowseCatalog` and
   `handleRefreshCatalog` (`internal/api/catalog.go`) will now have no source
   to list on a fresh install. Verify they return an empty result cleanly —
   **not an error**, and not a 500. An empty catalog is a correct state, not a
   failure.

## Non-goals

- **Do not build the empty-state UI.** That belongs to the separate UI/UX
  review workstream queued for after the freeze. This task's obligation is to
  make the backend state correct and to **write down the UI consequence
  clearly** (see Done means) so that review picks it up with the reasoning
  intact rather than rediscovering it.
- Do not add key provisioning, a signing pipeline, or a bundled public key.
  That is the option AD-05 rejected.
- Do not touch the install pipeline. `01/01` owns it and it is correct.
- Do not remove `catalog_sources` the table, the CRUD API, or
  `SetCatalogSourcePublicKey`. Operator-configured sources remain fully
  supported — that is the whole point.

## Done means

- The seed no longer inserts a catalog source; a fresh database has zero rows
  in `catalog_sources`.
- The existing-database question is answered explicitly in the Work log, with
  the reasoning, whichever way it went. If a migration was added, its number
  was re-derived from disk immediately before claiming it.
- A regression test asserts a freshly seeded database has no catalog source.
- `handleBrowseCatalog` against an empty `catalog_sources` returns an empty
  list and a success status — verified by a test, not by inspection.
- **The UI consequence is written down for the UI/UX workstream**, in the Work
  log and in a one-line note wherever that workstream is tracked: *the plugin
  catalog now has no source on a fresh install; this needs an empty state that
  explains an operator must add and key a source, not a blank list or an error
  screen.* The operator has confirmed this pattern exists elsewhere in the
  product — the note should say to match it rather than invent one.
- `go build ./...`, `go vet ./...`, `go test ./...` clean.

## Work log

## Review notes
