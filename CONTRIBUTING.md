# Contributing

How a change gets from your clone into `main`. This is deliberately short: most
of what you need is already written somewhere closer to the thing it describes,
and this page points at it rather than keeping a second copy that drifts.

## Before your first change

`README.md` has the clone-and-run steps. Do not skip `lefthook install` — a
tracked config installs no hooks by itself, and a clone that misses it has no
commit or push checks and says nothing about it.

`AGENTS.md` is the fastest orientation to the layout: which package does what,
and the boundaries that are not obvious from reading the code. It is written for
an agent working in the repo, which makes it unusually direct about the things
that bite.

## The sequence

1. **Branch.** `<type>/<short-slug>`, where the type matches what the change is
   — `feat`, `fix`, `docs`, `chore`. Nothing enforces this; it is what the
   history does.
2. **Change one thing.** A branch carrying two unrelated changes costs the
   reviewer the ability to accept one and question the other.
3. **Run the landing check** when the feature is done — not on every commit.
   `docs/verifying-a-change.md` covers which tier proves what, and it is worth
   reading once because the tiers overlap less than they appear to.
4. **Push, and open a pull request.** Pushes to `main` run a heavier gate than
   pushes to a branch; both are described in the same document.

Commit subjects follow the conventional-commit shape — a type, an optional
scope, a colon, then the summary. To see what types are actually in use rather
than trusting this sentence:

```
git log --no-merges -40 --format='%s' | grep -oE '^[a-z]+(\([a-z-]+\))?:' | sort -u
```

## What a pull request should carry

The reviewer was not there when you made the decisions. State what the change
does, what it deliberately leaves alone, and the evidence that it works — the
commands you ran and what came back, not a claim that it passes.

If a number appears in the description, put the command that produced it beside
it. A figure nobody can re-derive costs the reviewer more than it saves.

## The one that cannot be undone

**Migration numbers.** A duplicate migration number is this repo's single
unrecoverable failure class, and the rule and its rationale are in `AGENTS.md`
under Boundaries. Read that section before adding a file under
`internal/store/migrations/`. It also carries the rule about what a migration
may and may not contain, which is narrower than it first appears and has one
carefully-bounded exception.

The guard that enforces it runs on pushes to `main` with no file filter, and the
reason it carries no filter is written in the script's own header.

## Things that surprise people

- **`ui/src/generated/` is generator output.** Regenerate it; never hand-edit.
- **`go build` does not produce the binary the running service executes.** The
  deployment boundary is in `AGENTS.md`, and the failure mode — a deploy that
  leaves the old process running — is quiet.
- **The envelope catalog lives in a released module**, not in this repo. New
  core types are released there first.
- **`internal/context/INVARIANTS.md` and its test change together or not at
  all.** See `docs/architecture/context-assembly.md` for why the slot shape is
  less flexible than it looks.

## What this does not cover

- **Which change is worth making.** There is no roadmap here by design; that
  conversation happens in the tracker.
- **Release and deployment.** Separate subjects with separate mechanics.
- **Plugin authoring.** A plugin is a subprocess against a published interface,
  not a contribution to this repo.
