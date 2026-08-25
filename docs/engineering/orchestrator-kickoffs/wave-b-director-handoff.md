# Director handoff — Gate Integrity, Wave B onward

**For: me, after compaction.** Written 2026-08-25 at `eb182d71`. Every number
below ships the command that produced it. **Re-derive before acting; HEAD moves.**

---

## 0. My role changed. Read this first.

**I am no longer the Orchestrator. I am the director layer.**

A separate Claude session is the Orchestrator for Wave B. Operator decision,
2026-08-25. The split:

| Who | Does |
|---|---|
| **Operator + me** | Architecture, decisions, overall context and state. I hold the thread across waves. |
| **Orchestrator session** | Dispatches workers/reviewers, writes and updates task files, `TASKS/INDEX.md`, Work logs, Review notes. **They are the only writer of those.** |

**I do not write task files, `TASKS/INDEX.md`, Work logs, or Review notes any
more.** That was my job in Wave A and it is not now. If something in them needs
changing, I tell the Orchestrator; they make the edit and own it.

**I drive that session from here** via `SendMessage`. They escalate to me; I
work it with the operator; I reply back with the decision. I do not silently
decide things the operator should weigh in on — that is the whole point of this
topology.

**What I still do directly:** read the repo to verify claims, dispatch
read-only `research-auditor` agents when I need a claim checked independently,
and talk to the operator. **What I no longer do:** dispatch `worker` agents,
commit to the repo for batch work, or update tracking files.

### Two standing operator instructions that override the repo's own docs

1. **`TASKS/ESCALATIONS.md` is bypassed.** Do not read it, do not write it, do
   not instruct anyone to. `docs/engineering/EXECUTION-PROCESS.md`'s escalation
   section and the Wave A kickoff's read-list item 9 both point there — **ignore
   those.** Findings and escalations get documented **on the task files**.
   Operator, 2026-08-25: *"That file and the process that has evolved from it is
   not what was intended."*
2. **Decisions go to Tesseract**, not to a log file in the repo. Use
   `/capture-decision` (or `mcp__mux__memory_write` to
   `user/chrispian/memory/decisions`, tags `["nanite","gate-integrity"]`).
   Memory keys accept `a-z 0-9 _` per segment — hyphens are rejected.

---

## 1. What just closed — Wave A, all five reviewed

Verify the end state before trusting anything below:

```
git log --oneline 77137106..HEAD | wc -l          # 35 at eb182d71
grep -c '=>' go.mod                                # 0
grep -c 'actions/checkout' .github/workflows/full-repo-quality.yml   # 1
grep -c 'libs/' .github/workflows/full-repo-quality.yml              # 0
lefthook dump | sed -n '/^pre-push:/,$p'           # go-test: run + only: ref main, no glob
grep -c '^### 3\.' docs/engineering/agent-verification-discipline.md # 12
```

| Task | What it did |
|---|---|
| `08` | pre-commit is formatting only (3 staged-scoped commands); `./scripts/check.sh` is the landing check; pre-push runs on **every** push to `main` |
| `01` | dropped go-envelopes + go-modelsdev replaces, bumped go-envelopes `v0.1.1`→`v0.3.0` |
| `02` | cut and published `v0.1.1` for go-harness-filters and go-runtime-events (sibling repos) |
| `03` | **keystone** — dropped the last two replaces and the entire sibling-checkout block |
| `04a` | gosec reduction advisory now demands a repeat run before the baseline moves |

**Three green gate runs:** `32856005953` (`f08ac62a`), `32864133579`
(`834c9506`), `32878651576` (`eb182d71`, 17/17, covers everything).

**What `01`-`03` bought:** `git clone && go build` needs no
`hollis-labs/{apps,libs}` layout; worktrees work anywhere; a container needs
only source + proxy access; CI validates published releases.

Read `TASKS/gate-integrity/WAVE-A-SUMMARY.md` and `HANDOFF-TO-WAVE-B.md` for the
full picture — the doc-writer derived those independently.

---

## 2. What's next

### Wave B — `07-migration-number-collision-guard`. One task.

**This is the only task in Wave B**, and it guards the one *unrecoverable*
failure class. Nothing else in this batch is unrecoverable — everything else is
found on the next run and fixed in a follow-up. A bad migration number is a
service that fails to start on every existing deployment.

Why it is unrecoverable, verified:

```
grep -n 'WithAllowOutofOrder' internal/store/store.go    # absent
ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1  # 148_..., next free is 149
```

goose is built without `WithAllowOutofOrder`, so a migration numbered below a
database's highest applied version is a hard boot error, not a back-fill. `135`
is a permanently burned hole. **Next free is one past the highest, never the
lowest unused integer.**

`migration-purity` does **not** catch this and structurally cannot: it greps
staged migration *contents* for a `VALUES` clause and never reads a filename,
and two worktrees each see only their own staged file.

**`Touches`:** `lefthook.yml`, a new check script, `docs/engineering/tracking-integrity.md` (check 9).

### Wave C — container work. Not scoped, deliberately.

**Do not scope it until the frontend coupling below is settled.** The README is
explicit that its shape depends on the repo being self-contained, and it is not
yet — see §4.

### Wave D — `04b`, `05`, `06`.

`04b` = steps 1-5 of the `04` file (concurrency experiment, repeat-run agreement
wrapper, `Stats` coverage floor, missing-`Issues` failure, `Golang errors`
decision). `05` = runbook false-green. `06` = citation drift sweep.

---

## 3. Session topology — how to run this

**One Orchestrator for Wave B.** A second has nothing to dispatch, and `07` is
precisely the guard that makes parallel write-work safe. Parallelizing before it
lands is the risk it exists to prevent.

**After `07` lands, a second session becomes useful.** Collision analysis from
the task files' own `Touches`:

| Task | Touches | Parallel-safe with |
|---|---|---|
| `07` | `lefthook.yml`, new script, `tracking-integrity.md` | `04b`, `05` |
| `04b` | `quality-ratchet.py`, workflow gosec step, baseline, new wrapper | `07`, `05` |
| `05` | the runbook **only** | `07`, `04b` |
| `06` | `.golangci.yml`, audit config, `INDEX.md`, `PREVENTION.md`, a task file, `docs/engineering/README.md`, `AGENTS.md` | **nothing** |

**`06` must run last and alone.** It is a citation-drift sweep — its *input* is
the state of every other document, so any concurrent write invalidates its
derivations mid-flight. This is not a merge-conflict concern; it is a
correctness one.

**If a second session is booted:** each Orchestrator owns its own task files.
`TASKS/INDEX.md` is a shared mutable tracker — two writers on it is the failure
this project has already hit. Either one session owns `INDEX.md` outright, or
they hand me the text and I relay. Decide before dispatching, not after.

---

## 4. Open items I carry

**The frontend is not decoupled — the most important open item.** `01`-`03`
made the repo self-contained *for Go only*. Three call sites hardcode
`../../libs/go-envelopes`:

```
grep -n "libs.*go-envelopes" scripts/generate-plugin-imports.mjs \
  scripts/generate-envelope-types.mjs Makefile
#   generate-envelope-types.mjs:36   generate-plugin-imports.mjs:23
#   Makefile:24,27,31
```

`ui/package.json`'s `prebuild`/`predev` run two of them, and `make install`
depends on `generate-envelopes`, so `npm run build`, `npm run dev` and
`make install` all still require the sibling tree. **Wave C's container work
depends on this.** `generate-envelope-types.mjs` already accepts
`--manifest-dir`; `generate-plugin-imports.mjs` has no equivalent. Likely fix:
`go list -m -f '{{.Dir}}' github.com/hollis-labs/go-envelopes`. Not scoped to
anyone yet — operator decision pending.

**Sibling CHANGELOG commits unpushed.** Both repos sit `[ahead 1]`:

```
git -C ~/dev/hollis-labs/libs/go-harness-filters status -sb | head -1
git -C ~/dev/hollis-labs/libs/go-runtime-events  status -sb | head -1
```

Tags are published; the changelog commits are not. Operator authorized tag
pushes only. Two `git push origin main` commands when they say so.

**Owned elsewhere, do not re-raise:**
- Sibling-repo CI has never passed in either repo, including at `v0.1.0` —
  operator's parallel lib-audit session owns this.
- `/private/tmp` symlink cleanup — Torque `CW-20260825-0019`.
- cerberus and hadron relative replaces — **both already fixed and verified.**
  No app in the portfolio carries one now.

**Cosmetic, parked:** `lefthook validate` exits 1 on three pre-existing
`skip_empty` keys. Nothing runs `validate` automatically.

---

## 5. Hazards learned this session — `agent-verification-discipline.md` §3

Five landed (`3.8`-`3.12`). The two that will bite Wave B hardest:

- **§3.11 — `GOPRIVATE` defeats a `GOPROXY=` prefix.** `go env GOPRIVATE` is
  `github.com/hollis-labs/*`, which defaults `GONOPROXY` **and `GONOSUMDB`** to
  the same value. So `GOPROXY=... go list -m -versions` never contacts the
  proxy, and a local `go.sum`/`GOMODCACHE` is not evidence about published
  bytes. Ask the proxy over HTTP; clear overrides with `=none`, not empty.
- **§3.12 — verify a "path does not exist" proof at the *resolved* path.**
  Depth does not establish absence once symlinks or `/tmp` → `/private/tmp` are
  in play.

Also: **`@v/list` is a cached view that lags**; `@v/<version>.info` returning 200
is the sharper confirmation that something is published. This produced a false
alarm on hadron today.

---

## 6. What went wrong in Wave A that I should not repeat

- **I marked `03` `reviewed` with its Review notes section empty** — an
  approval with no record. Under the new topology the Orchestrator writes those,
  so my job is to *check they exist* before accepting a `reviewed` claim.
- **I ran `git add -A` while a worker was live** and swallowed its work into an
  unrelated commit. Stage explicit paths, always.
- **I propagated a claim without checking it** (that two standards docs were
  empty stubs) and nearly put it in a handoff. Verify before relaying — that is
  most of my job now.
- **I wrote two errors into a hazard entry** that a later worker had to fix. A
  hazard whose remedy is inert is worse than no hazard.

Pattern: every one was caught by an agent I dispatched, not by me. **Keep
dispatching independent verification.** `research-auditor` is read-only, cannot
dispatch further agents, and is cheap.

---

## 7. First actions after compaction

1. `git rev-parse --short HEAD` and `git status --short` — confirm where things
   stand. Expect a clean tree at or after `eb182d71`.
2. `ListAgents` — find the Orchestrator session the operator booted.
3. Read `TASKS/gate-integrity/HANDOFF-TO-WAVE-B.md` — the doc-writer's version,
   independently derived.
4. Ask the operator whether Wave B is authorized to start, and confirm the
   Orchestrator has read the handoff. **Do not dispatch anything myself.**
