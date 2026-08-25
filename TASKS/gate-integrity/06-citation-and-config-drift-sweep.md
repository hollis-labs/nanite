# Citation and config drift sweep — one pass over eight items

**Phase:** 1 — Measurement integrity
**Status:** not-started
**Depends on:** none
**Touches:** `.golangci.yml`, `docs/audits/2026-08-21-go-quality/audit-golangci.yml`,
`TASKS/INDEX.md` (item 8 only, and only if it turns out to need it),
`TASKS/audit-remediation/PREVENTION.md`,
`TASKS/audit-remediation/12-quality-ratchet-and-standards/01-full-repo-scheduled-lint-gate.md`,
`docs/engineering/README.md`, `AGENTS.md`. Repo: nanite.

Deliberately batched into one task. Each item is a few minutes; filed
individually they would cost more in dispatch overhead than in work, and they
share a single reviewer concern (is the new citation correct *now*).

## Context

Every item below was verified against live code at `77137106` by the
post-unfreeze planning session. Two of them correct the register this batch
came from — the register's own numbers had drifted.

**Re-derive every one of these before editing.** They are citations about
citations; the class of error being fixed is exactly the class most likely to
recur while fixing it.

### 1. `.golangci.yml` pins a stale Go version

```
sed -n '20,24p' .golangci.yml      # run: / go: "1.25"
grep -n '^go ' go.mod              # go 1.26.7
```

Same stale pin was already corrected in the audit config; this is the copy
that was missed.

### 2. `audit-golangci.yml:4` cites workflow lines that moved

Its header says `.github/workflows/full-repo-quality.yml:111,114` passes it to
`golangci-lint linters` and `golangci-lint run`. At `77137106` those are
**127 and 130**:

```
grep -n 'config docs/audits/2026-08-21-go-quality/audit-golangci.yml' \
  .github/workflows/full-repo-quality.yml
```

This file's header opens with "GATE CONFIG — EDITING THIS FILE MOVES THE
QUALITY RATCHET," so a wrong pointer in it is worse than a wrong pointer in a
doc.

### 3-4. Two files cite `lefthook.yml:3` for a claim that line no longer makes

```
grep -rn '15 seconds on staged files' TASKS/ | grep -v node_modules
```

returns `TASKS/audit-remediation/PREVENTION.md:161` and
`TASKS/audit-remediation/12-quality-ratchet-and-standards/01-full-repo-scheduled-lint-gate.md:272`,
both attributing *"complete in <15 seconds on staged files only"* to
`lefthook.yml:3`.

**Both the pointer and the substance are wrong now.** Line 3 of `lefthook.yml`
is a blank comment line, and per `sed -n '9,12p' lefthook.yml` only three of
the five pre-commit commands are staged-only — `go-vet` runs `go vet ./...`
whole-repo and `go-lint` analyzes the whole repo before filtering its report.
`lefthook.yml`'s own measured header now carries real numbers (44.87s cold /
4.32s fully cached for pre-push) — use what the file says, re-derived, not
this paragraph's copy of it.

Judgment call for the worker, stated as adjustable rather than decided:
`PREVENTION.md` is a live prevention document and should be corrected.
The `12/01` task file is a **landed work artifact** — the batch README's scope
fence keeps historical Work Logs untouched for exactly this reason. If `12/01`'s
line 272 reads as a live claim rather than a record of what was believed at
execution time, fix it; if it reads as history, annotate rather than rewrite.
Make the call, and record which you chose and why.

### 5. `docs/engineering/README.md`'s index is incomplete

The register says "~10 files" omitted. **The register is wrong** — re-derive:

```
ls docs/engineering/*.md | sed 's|.*/||' | sort > /tmp/actual.txt
grep -oE '[a-zA-Z0-9._-]+\.md' docs/engineering/README.md | sort -u > /tmp/indexed.txt
comm -23 /tmp/actual.txt /tmp/indexed.txt
```

At `77137106` that returns **8 entries, one of which is `README.md` itself**,
so 7 real omissions — including `tracking-integrity.md`, which two of the
newly-promoted engineering docs name as a companion. Do not carry either "10"
or "7" into the edit; run the command.

### 6. `AGENTS.md` describes boot profiles as live

```
grep -n -i 'boot.profile' AGENTS.md      # -> line 57 at 77137106
```

The boot-profile catalog was retired in full (`TASKS/phase-2/04`). `CLAUDE.md`
already records the retirement and what carried forward in its place — dynamic
context resolvers and the mandatory post-compaction re-read. **Write what is
true now; do not narrate the retirement.** A reader of `AGENTS.md` needs the
current mechanism, not its history.

### 7. PATH `gofmt` is a different Go than the build — decide, do not just note

```
command -v gofmt                                     # mise go 1.25.3
go version                                           # go1.26.7
```

This one is not a citation fix and may not be a repo fix at all: `go-format`
is a live pre-commit hook, so the hook formats with a different Go release
than the build compiles with. Establish first whether 1.25.3 and 1.26.7 gofmt
actually disagree on this tree — if they do not, this is a note; if they do,
it is a real hazard and belongs in
`docs/engineering/agent-verification-discipline.md` §3, whose environment-hazard
list is explicitly append-only for findings like this. Do not change the
developer's mise config from a task file.

### 8. `TASKS/INDEX.md` says the first green run covered "all 18 steps" — the workflow has 17

```
grep -n '18 steps' TASKS/INDEX.md                                  # -> line 63
grep -c '^      - name:' .github/workflows/full-repo-quality.yml   # -> 17
```

**This one may be a false positive — check before "fixing" it.** GitHub's
Actions UI counts implicit `Set up job` / `Complete job` / post-steps that do
not appear in the YAML, so 18 may be an accurate count of what the run page
displayed rather than a drifted number. Open run
[`32791971817`](https://github.com/hollis-labs/nanite/actions/runs/32791971817)
and count what it actually shows.

If the run page shows 18, leave the claim and add a parenthetical naming which
count it is — an unqualified number that disagrees with the YAML will be
re-reported as drift by the next reader, which is its own cost. If it shows 17,
correct it. Either way the outcome is a number a reader can reconcile.

## What to do

Work items 1-8 above. For each: re-derive the citation, make the smallest
correct edit, and record the command you re-derived with in the Work log. Where
an item turns out to be already fixed or not to reproduce, say so — that is a
result, not a skipped item.

## Done means

- Items 1-6 and 8 each resolved by re-running that item's own derivation command
  and getting the corrected answer, with commands and outputs in the Work log.
- `.golangci.yml`'s `go:` matches `go.mod`'s `go` directive, both re-derived.
- No file in the repo attributes a "<15 seconds on staged files only" claim to
  `lefthook.yml:3`:
  `grep -rn '15 seconds on staged files' --include='*.md' . | grep -v node_modules`
  returns only what the item 3-4 judgment call deliberately preserved, and the
  Work log says which and why.
- The `comm` command in item 5 returns only `README.md`.
- `AGENTS.md` describes the current mechanism and contains no claim that boot
  profiles are live.
- Items 7 and 8 each reach a stated verdict — hazard or non-issue, drifted or
  accurate — with the evidence that produced it. If item 7 is a hazard, the §3
  append landed. Recording "checked, not a drift" is a completed item, not a
  skipped one.
- `go vet ./...` and `golangci-lint run --new-from-rev HEAD` clean, since
  `.golangci.yml` changed.

## Work log

## Review notes
