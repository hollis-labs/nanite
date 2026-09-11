# Documentation doctrine

What a file in this repo may contain, and who it is for. Read this before
writing or moving a documentation file.

This is not a style guide. Voice, structure and phrasing are a separate
subject; this settles **content and placement** only. Every rule below is meant
to be applied to a diff without a follow-up question — if one of them leaves you
asking, that is a defect here, not in your change.

Most of this **records what the repo already does** rather than imposing
something new. That is why adopting it moves no files.

## Audience is decided by location, not declared in the file

A file's directory says which audience it serves:

| Location | Audience | Who that is |
|---|---|---|
| Repository root | **USER** | someone running Nanite |
| `docs/` | **DEVELOPER** | someone changing Nanite |
| `AGENTS.md`, `CLAUDE.md`, `.claude/` | **AGENT** | an agent working in this repo |

A file in the wrong directory is moved, not annotated. Location is checkable at
a glance and survives editing; a declared audience is one more claim that can
go stale while the file sits still.

**The conventional root files are placed by name, not by audience.** `README`,
`LICENSE`, `CHANGELOG`, `CONTRIBUTING`, `SECURITY`, `CODE_OF_CONDUCT` and the
like live at the root because the ecosystem fixed both the name and the place —
tooling looks for them there, and so does anyone arriving from another
repository. `CONTRIBUTING.md` is developer-facing and sits at the root anyway;
that is the convention working, not the rule breaking.

This is a closed set, and it is the one place a filename carries the audience
instead of the directory. It stays closed for the same reason the rest of the
rule exists: a project-invented file at the root is just a file in the wrong
place with a confident name.

`README.md` is the front door, and it is where this goes wrong most easily: its
Quick Start opens on `lefthook install` and `go build`, which is developer
onboarding standing in the user's doorway. A conventional file being at the root
says where the ecosystem expects to find it; it does not license the *contents*
of the front door to drift developer-ward.

Operators — people running Nanite in a deployment without changing it — are
**DEVELOPER** here. The word appears in `README.md`, routing readers to the
Tesseract migration guide, and it is deliberately not a fourth audience: one
more bucket costs more than it settles.

## No state in files

State is anything whose truth can change without the file being edited: status,
progress, completion claims, "as of" dates, counts, what is currently
implemented, who is working on what.

A file has no coordination mechanism. Two people reading the same sentence a
month apart get different truths and neither can tell which they got. Worse,
the failure is silent and runs in **both** directions — a file claiming
something is unfinished when it shipped is exactly as wrong as one claiming
completion that never landed, and neither announces itself.

State belongs in the tracker. Durable decisions belong in the knowledge store.
Files carry what is true by construction.

A sentence like *"this document should always reflect the current state of the
system"* is itself the defect: it stores an intention where a fact is expected
and nothing enforces it.

## Prefer a materialized fact to a paragraph — and make sure the fact is checkable

A table, a command with its output, or a reference to code beats a paragraph
asserting the same thing. A paragraph rots quietly; a command that stops
working announces itself. Where prose is unavoidable, prose describing intent
ages better than prose describing state.

**This rule qualifies itself.** A command that can fail silently is worse than
the paragraph it replaced, because it *looks* verified. A zero is the dangerous
case: it reads as "checked, nothing there" whether or not the query could ever
have matched.

A real instance, in the tool this repo reaches for most: `git grep -E` does not
support `\b`. It returns no matches rather than an error.

```
git grep -ohE '\b(CW|EP)-[0-9-]+' -- CHANGELOG.md   # 0 — silently wrong
     grep -ohE '\b(CW|EP)-[0-9-]+'    CHANGELOG.md   # 6 — the real answer
```

So: before a zero is written down, run the query against something that *must*
match. If the control returns nothing either, the query is broken, not the
tree.

This is also the one rule here whose supporting evidence is the document set
archived out of this repo, which can no longer be cited. It is kept on that
basis rather than on current practice, and says so instead of implying
otherwise.

## No `file:line` citations in a living document

Cite a file and a symbol — `` `ListAgentReflexesForAgent`, in
`internal/store/agent_reflexes.go` `` — not `` `agent_reflexes.go:378` ``.

A line number describes the tree it was read in, not the commit. The case
people fail to anticipate is their own: an edit in the same session moves the
passage and the citation is stale before the file is saved. "Someone else moved
it" is intuitive; "I moved it" is not.

Where a location genuinely must be precise, ship the command that finds it
rather than the coordinates it returned.

## Tracker identifiers never appear in user-facing or shipped text

Ticket, epic, project and internal design identifiers — `CW-…`, `EP-…`,
`PRJ-…`, `AD-…`, `GO-…` — and internal batch, wave or phase names. They resolve
to nothing a reader outside this project can open, so they are noise at best
and a dead end at worst.

`CHANGELOG.md` is the standing violation: it is declared user-facing in its own
first line and carries these identifiers in release notes.

To find them, describe the shape rather than listing the ones you remember. An
enumeration is only as complete as the memory that produced it, and this
particular class has been under-counted every time it was listed instead of
matched.

## Sibling-product names appear when the reader depends on them

The counterpart to the rule above, and not the same rule. A product Nanite
actually embeds or talks to is something the reader has to know about: naming
Tesseract in a Tesseract migration guide is the document doing its job.

The test is whether the reader can act on the name. A dependency they compile
against, configure or upgrade: name it. An internal tool, workspace or tracker
they have no access to: leave it out. A name that is merely *stale* — a
dependency renamed since the text was written — fails this test too, because
the reader cannot act on a name that no longer resolves.

## A document is frozen by where it lives, never by what it says

Mark a document historical by moving it into an archive. Do not add a banner, a
status correction, or a "frozen" header — and do not name a file for a date and
leave it in `docs/`. A file needing a date to make sense is a record.

This is a mechanism, not a preference:

- Annotating means editing every file to add a claim, and every edit is a chance
  to make a record say something its author did not.
- A banner is itself state written into a file, which is the previous rule
  failing one level up. Freezing a document by writing "this is frozen" into it
  reproduces the problem it was meant to solve.
- A container scales for free. Anything moved in later is covered with no
  further edit.

The worked example is real. An archived debugging note was headed UNRESOLVED
with its own fix already landed, and the archival pass corrected the status in
place — reasoning that a false open bug is a trap for whoever mines the
archive. The trap was real; the remedy put a new claim into a file nobody
maintains, which is how the trap got there. The edit was reverted and the
finding went to the tracker, which is where a fact about an archived document
belongs.

## Shipped product has different rules, not no rules

`internal/assets/framework/` is embedded into the binary and installed into
users' projects. It is product, not documentation about this repo, and it is
governed accordingly: it may name its own commands, paths and tools freely,
because those are the thing it ships.

It is not exempt from the rest. Tracker identifiers must not ship, and neither
may state claims — both travel into every project that installs the framework,
where they are further from repair than they are here. Placeholders (`CW-ID`,
`CW-XXX`) are fine; they are syntax, not references.

## What this does not cover

- **Voice, tone, structure and formatting.** A different subject with its own
  home.
- **Where a specific document should live.** This gives the rule; the file's
  own task makes the call.
- **The archive.** Once a document is out of this repo, this doctrine has
  nothing further to say about it.
- **Whether a document should exist.** These rules constrain what a file may
  contain, not what is worth writing.
