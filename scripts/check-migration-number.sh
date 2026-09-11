#!/usr/bin/env bash
# scripts/check-migration-number.sh — the migration-number guard.
#
# This is the mechanical form of the tracking-integrity rule on claimed
# identifiers: a migration number must be strictly greater than the highest in
# use on the shared branch, and must collide with none already there. The rule
# is stated wherever that discipline is written down; this script enforces it.
#
# ── Why this is the one unrecoverable failure class ───────────────────────
# `internal/store/store.go` builds goose without `WithAllowOutofOrder`, so
# `allowMissing` is false. Derive that citation rather than trusting it:
#     grep -n 'goose.NewProvider' internal/store/store.go
#
# Reusing a number at or below a database's max applied version has **two**
# outcomes, and which one you get is decided entirely by that database's
# vintage — not by anything about the file. Read
# `internal/gooseutil/resolve.go` at the version `go.mod` pins (v3.27.3); the
# ledger map is keyed by version number alone, with no filename and no
# checksum:
#     dbAppliedVersions := make(map[int64]bool, len(dbVersions))   // :30
#     if dbAppliedVersions[v] { continue }                         // :47 missing loop
#     if dbAppliedVersions[v] { continue }                         // :73 new loop
#
#   * Number NOT in the ledger, and below its max -> it lands in `missing`,
#     and `len(missing) > 0 && !allowMissing` returns `newMissingError`:
#     `detected 1 missing (out-of-order) migration lower than database
#     version (N): version M`. `Store.migrate` surfaces it and **the service
#     does not boot**, at startup.
#   * Number ALREADY in the ledger -> **both** loops `continue`. It is never
#     added to `missing` and never added to `out`, so goose returns cleanly and
#     reports itself up to date. **The migration silently never runs.**
#
# So the framing "a hard boot error, not a back-fill" is only half of it, and
# the half it omits is the more dangerous one: a schema that has quietly
# diverged while goose reports healthy. `135` is the canonical example of both
# — a database that applied the old `135_goals.sql` before `63d79028`
# renumbered it takes the silent path; a database created after takes the boot
# error. Same file, same number, opposite symptom. Anyone diagnosing a real
# incident from the "it fails to boot" framing alone would sit waiting for a
# startup error that never arrives.
#
# Everything else the checks catch is directional and recoverable. This is not.
#
# ── The two failure shapes ────────────────────────────────────────────────
#   1. Collision — two worktrees both claim the next number.
#   2. Back-fill into a burned hole — a number below the highest that merely
#      *looks* free. `135` has been empty since `63d79028` renumbered the Loops
#      batch's `135`-`143` up to `138`-`146`. It is not available and no hole
#      ever is. **"Next free" is one past the highest, never the lowest unused
#      integer.**
# A uniqueness-only check passes shape 2 happily, so both are asserted here.
#
# ── Why it compares against the *remote's* main, not local `main` ─────────
# Work in this repo happens on `main` directly, so at pre-push time local
# `main` already contains the commit being pushed. Comparing against it inverts
# both assertions — the new number *is* present, and it *is* the highest, so
# not strictly greater — and every legitimate migration would be rejected. The
# question that matters is what is on the remote.
#
# `git ls-remote` answers that authoritatively with no fetch and without
# depending on anything inside lefthook. `origin/main` is then used for the
# tree read, because that is the ref whose objects we actually have locally —
# but only after proving it equals the remote. A stale `origin/main` is a
# fail-open, so a mismatch fails closed with a `git fetch` instruction.
#
# ── Fails closed, never silently — with one stated exception ─────────────
# Unreachable remote, missing branch, stale tracking ref, a path that will not
# parse, or an empty migration set on the remote each abort loudly. A guard that
# no-ops when it cannot see the remote is worse than no guard, because it reads
# as protection. There is no skip branch anywhere in this file: any path the
# tree read or the diff returns must parse as `NNN_name.sql` or the run is a
# hard error. See `migration_number()` for why that is worth the strictness.
#
# The empty-set abort is deliberately a hard error rather than a vacuous pass:
# an empty result is also what a wrong `MIG_DIR` looks like, and this repo
# demonstrably has migrations on `main`. That assertion sits in section 1
# **ahead of the offline short-circuit**, which is the only position where it
# actually runs on every invocation — a `git diff` whose pathspec matches
# nothing exits 0 with empty output, so behind the short-circuit it would never
# fire and a wrong `MIG_DIR` would silently check nothing. Do not move it.
#
# THE EXCEPTION, stated because "fails closed, never silently" otherwise reads
# as covering it and does not: this command is gated on `only: - ref: main`,
# which lefthook evaluates against the **current branch**. `git push origin
# feature:main` from a non-`main` branch therefore prints
# `migration-number (skip) by condition` and lands on `main` unchecked. That is
# the ref-gate design working as specified and is not a bug in this script —
# but it is the same "reads as a benign, correct skip" shape that the no-`glob`
# ruling exists to prevent, arriving through the branch condition instead. It is
# an accepted, documented gap: the guard covers pushes made from `main`, and
# a push that renames a branch onto `main` is outside what a ref gate can see.
#
# ── Offline pushes that add no migration are not blocked ─────────────────
# The local groundwork in section 1 runs first, and an empty added-set exits 0
# without ever contacting the remote. So a docs-only push from a plane still
# works. The network is required only once a migration is actually in the push,
# which is the only time the remote's state can matter. Section 1 carries the
# argument for why that short-circuit is not a fail-open, and the premise it
# rests on that is *not* actually true; read both before changing this order.
#
# ── Paths are read NUL-delimited ─────────────────────────────────────────
# Both git reads use `-z`. Git's default `core.quotePath=true` C-escapes and
# double-quotes any path containing a non-ASCII byte, `"`, `\` or a tab, so a
# real migration named `135_café.sql` arrives from `--name-only` as
# `"internal/store/migrations/135_caf\303\251.sql"`. That is unset both locally
# and globally in this repo, so the quoting configuration is the shipping one on
# every clone. `-z` was chosen over `-c core.quotePath=false` because the config
# flag fixes non-ASCII but still leaves a filename containing a literal newline
# breaking a line-based read; `-z` closes both.
#
# Scope: this only protects clones where `lefthook install` has run. That is a
# real residual gap and this script does not close it. Confirm with
# `find .git/hooks -type f ! -name '*.sample'`.
#
# Usage: no arguments. Run standalone from anywhere in the work tree, or via
# lefthook's `pre-push` `migration-number` command.

set -uo pipefail

# Scratch files for NUL-delimited git output. `-z` is what makes this guard
# safe against mangled paths (see the reader helpers below), but NUL cannot
# survive a shell variable: bash silently strips it from a command
# substitution, so `x=$(git ls-tree -z ...)` concatenates every path into one
# string. Verified — `bash -c 'v=$(printf "a\0b\0"); printf "%s" "$v"'` yields
# `ab`. So `-z` output goes to a file and is read with `read -d ''`.
_mig_tmpdir=$(mktemp -d 2>/dev/null) || {
  echo "ERROR: migration-number guard could not create a temp dir; refusing to"
  echo "       run rather than checking nothing."
  exit 1
}
trap 'rm -rf "$_mig_tmpdir"' EXIT
REMOTE_LIST="${_mig_tmpdir}/remote"
ADDED_LIST="${_mig_tmpdir}/added"

MIG_DIR="internal/store/migrations"
REMOTE="origin"
BRANCH="main"
REMOTE_REF="refs/heads/${BRANCH}"
TRACKING="refs/remotes/${REMOTE}/${BRANCH}"

root=$(git rev-parse --show-toplevel 2>&1)
root_status=$?
if [ "$root_status" -ne 0 ]; then
  echo "ERROR: migration-number guard: not inside a git work tree."
  echo "       git rev-parse --show-toplevel said: ${root}"
  exit 1
fi
cd "$root" || exit 1

# Every abort that means "I could not establish a comparison ref" goes through
# here, so none of them can be mistaken for a pass.
fail_ref() {
  echo "ERROR: migration-number guard could not establish a comparison ref."
  echo
  echo "  ${1}"
  echo
  echo "  This guard compares every added ${MIG_DIR}/NNN_*.sql against"
  echo "  ${REMOTE}/${BRANCH}'s tree. Without that tree it cannot tell a fresh"
  echo "  number from a collision or from a back-fill into a burned hole, so it"
  echo "  fails closed rather than passing you silently."
  echo "  The rule it enforces: a migration number must be strictly greater"
  echo "  than the highest in use on ${REMOTE}/${BRANCH}, and must collide with"
  echo "  none already there."
  echo
  echo "  Fix:    ${2}"
  echo "  Bypass: only after checking by hand that no migration number in this"
  echo "          push is at or below ${REMOTE}/${BRANCH}'s highest."
  exit 1
}

# Extract the numeric prefix of a migration path. Echoes the integer with
# leading zeros stripped; echoes nothing and returns 1 if the basename is not
# `NNN_something.sql`.
#
# THERE IS NO SKIP BRANCH, AND THAT IS THE POINT. An earlier version skipped
# anything not ending in `.sql`, on the theory that a stray README in the
# migrations directory should not brick the guard. That branch was a hole: git
# quotes any path containing a non-ASCII byte (or `"`, `\`, tab) in
# `--name-only` output, so `135_café.sql` arrived as
# `"internal/store/migrations/135_caf\303\251.sql"` — a basename ending in `"`,
# which failed the `.sql` test and took the skip. Exit 0, zero bytes of output,
# a `135` back-fill accepted and pushed.
#
# The irony is worth keeping: the unparsable-name hard error below exists
# precisely so this guard never guesses, and path quoting routed a real
# migration *around* it into the skip. Reading paths with `-z` fixes that
# trigger; deleting the skip is what kills the class, so the next unknown
# mangling fails loud instead of passing a back-fill.
#
# Nothing legitimate is lost. The diff and the tree read are both pathspec-
# restricted to $MIG_DIR, so every path they return is by construction a
# migration, and no non-`.sql` file has ever existed there:
#     git ls-files internal/store/migrations/ | grep -cv '\.sql$'   -> 0 of 147
#     git log --all --diff-filter=A --name-only --format='' \
#       -- internal/store/migrations/ | grep -v '\.sql$' | grep -v '^$'  -> empty
#     (positive control, .sql ever added: 183)
#
# No `sort` anywhere in this script, deliberately. `sort -t_ -k1 -n` over
# *paths* is a dead key — field 1 is the whole `internal/store/migrations/148`
# prefix, which `-n` reads as 0 for every line, so ordering falls through to a
# byte comparison and `9_a.sql` outranks `148_b.sql`. Bounding the field
# (`-k1,1`) does not fix it. Extracting the integer and comparing with `-gt` has
# no dependency on prefix width, on `sort`'s implementation, or on locale, and
# the parse has to happen anyway for the equality test.
migration_number() {
  local base="${1##*/}"
  case "$base" in
    *.sql) ;;
    *) return 1 ;;
  esac
  local num="${base%%_*}"
  case "$num" in
    ''|*[!0-9]*) return 1 ;;
  esac
  # 10# forces base 10: $((008)) is an arithmetic error, $((10#008)) is 8.
  echo "$((10#$num))"
}

# Every path that reaches the parser and fails it is a hard error. Callers pass
# the site so the message says where it came from.
reject_unparsable() {
  echo "ERROR: migration-number guard cannot parse a migration path${2}:"
  printf '       %s\n' "$1"
  echo "       Expected NNN_name.sql — goose derives the version from that"
  echo "       prefix. This is a hard error and never a skip: a path this guard"
  echo "       cannot read is a path it cannot clear, and silently ignoring one"
  echo "       is how a back-fill gets through."
  echo "       Rule: a migration number must be strictly greater than the"
  echo "       highest in use on the shared branch, and collide with none"
  echo "       already there."
}

# ── 1. Local groundwork — no network, so an offline push still works ──────
# Everything in this section reads local refs only. The network is not touched
# until section 2, and section 2 is reached only when the push actually adds a
# migration. A docs-only push from a plane therefore still works.
#
# WHY THE SHORT-CIRCUIT AT THE END OF THIS SECTION IS NOT A FAIL-OPEN. It reads
# as one until you have the argument. A local tracking ref only ever advances by
# fetching *from* the remote, so local `origin/main` is normally an ancestor of
# the remote's `main`; migrations are, in normal operation, only added. Under
# those conditions `files(local origin/main)` ⊆ `files(remote main)`, and since
# the added set is `files(HEAD) \ files(TRACKING)`, a *smaller* `files(TRACKING)`
# can only make it **larger**. "Added relative to local `origin/main`" is a
# **superset** of "added relative to the remote": a stale ref can over-report,
# never under-report. Over-reporting is harmless — it routes down the
# authoritative path below, which fails closed on staleness.
#
# THE APPEND-ONLY PREMISE IS NOT ACTUALLY TRUE, so do not read it as verified.
# It is the premise the superset argument above rests on, and this repo violates
# it twice in its own history. Re-derive both rather than trusting these:
#
#   * `63d79028` — the very commit that created the burned `135` hole this
#     guard exists to defend — removed 9 paths:
#         git diff --no-renames --diff-filter=D --name-only 63d79028^ 63d79028 \
#           -- internal/store/migrations/ | wc -l                          -> 9
#     (It shows 0 *with* rename detection and 9 without; either way the paths
#     stopped existing, which is what the premise claims never happens.)
#   * `efee58da` took the highest number from `027` down to `001`:
#         git ls-tree --name-only -z efee58da^ -- internal/store/migrations/ \
#           | tr '\0' '\n' | sed 's#.*/##;s/_.*//' | sort -n | tail -1     -> 027
#         ...same against efee58da                                         -> 001
#     Three commits have deleted from this directory in total:
#         git log --diff-filter=D --format='%h' -- internal/store/migrations/ \
#           | wc -l                                                        -> 3
#
# THIS GUARD STRUCTURALLY CANNOT SEE THAT CASE. Once a file is gone from the
# remote, its number is absent from `remote_nums` and no longer contributes to
# `remote_max`, so neither the membership check nor the highest-number check has
# any signal left to work with. That is different in kind from the burned hole,
# which the guard *does* catch precisely because the max still remembers it.
#
# THE CONSEQUENCE, verified by reading goose rather than assumed. Reusing a
# number that a database has already applied is **not** the boot error described
# at the top of this file. `internal/gooseutil/resolve.go` keys its ledger map
# by version number alone — no filename, no checksum — so both of its loops
# `continue` on an already-applied version (`:47`, `:73`); the migration is
# added to neither `missing` nor `out`, and goose returns cleanly reporting
# itself up to date. The new migration never runs. The result is **silent schema
# divergence**: databases old enough to have applied the deleted number quietly
# lack the change while goose reports healthy, and databases created afterwards
# (goose seeds version 0) apply it normally. Nothing here is a claim about any
# particular deployment — it follows from the source and holds for every
# database.
#
# It stays LOW, and here is why, so nobody re-escalates it: reaching this state
# requires deleting the current highest migration from `main`, or force-pushing
# `main` backwards. Both are violations of the claiming rule that are
# independently catastrophic and far outside what a pre-push hook can observe.
# Recorded so the next reader does not have to re-derive it, and does not
# mistake the premise for a proof.
#
# Two boundaries stay fail-closed and must not widen:
#   * a missing or unresolvable $TRACKING is NOT the empty case;
#   * either git command failing for any reason is NOT the empty case.
# Only a *successful* diff yielding an empty set may short-circuit.

# $TRACKING must resolve. A purely local ref lookup, so it works offline; a
# fresh clone that has never fetched fails here rather than short-circuiting
# past the guard.
tracking_sha=$(git rev-parse --verify --quiet "$TRACKING")
if [ -z "$tracking_sha" ]; then
  fail_ref "no local ${REMOTE}/${BRANCH} ref — nothing to compare against." \
    "git fetch ${REMOTE} ${BRANCH}"
fi

# The remote's migration set, read from the *local* tracking ref — `ls-tree`
# needs no network. It is read here, before the short-circuit, so that the
# non-empty assertion below actually runs on every invocation.
#
# `-z` is load-bearing, not tidying. Without it git C-escapes and double-quotes
# any path containing a non-ASCII byte, `"`, `\` or a tab, so `135_café.sql`
# arrives as `"internal/store/migrations/135_caf\303\251.sql"`. `-z` emits raw
# NUL-delimited paths and also survives a filename containing a literal newline,
# which `-c core.quotePath=false` would not.
lt_err=$(git ls-tree --name-only -z "$TRACKING" -- "${MIG_DIR}/" 2>&1 >"$REMOTE_LIST")
lt_status=$?
if [ "$lt_status" -ne 0 ]; then
  fail_ref "\`git ls-tree --name-only -z ${TRACKING} -- ${MIG_DIR}/\` failed (exit ${lt_status}):
  $(printf '%s' "$lt_err" | sed 's/^/  /')" \
    "check that ${MIG_DIR}/ exists on ${REMOTE}/${BRANCH}."
fi

# THE POSITIVE CONTROL. This must stay ahead of the short-circuit below.
# An empty set makes every assertion in this script vacuously true, and it is
# also exactly what a wrong MIG_DIR — a typo, or the directory moving in a
# refactor — looks like. A pathspec that matches nothing makes `git diff` exit 0
# with empty output, so without this assertion a wrong MIG_DIR would sail
# straight through the short-circuit and the guard would silently check nothing.
# That regression was real: it was introduced when this section was reordered to
# put the offline short-circuit first, and it is the reason the tree read moved
# up here.
if [ ! -s "$REMOTE_LIST" ]; then
  fail_ref "${REMOTE}/${BRANCH} lists no files under ${MIG_DIR}/.
      An empty set makes every assertion below vacuously true, so it is a hard
      error here rather than a pass. It is also exactly what a wrong path
      constant or a wrong ref looks like." \
    "confirm \`git ls-tree --name-only ${TRACKING} -- ${MIG_DIR}/\` lists the
          migrations, then fix MIG_DIR in scripts/check-migration-number.sh."
fi

# What this push adds. `--no-renames` on purpose: with rename detection on,
# renumbering an existing migration (`git mv 135_x.sql 138_x.sql` — exactly what
# `63d79028` did) is reported as R and `--diff-filter=A` drops it. A renumber
# *is* a claim of a new number. `--no-renames` splits it into D + A so the A half
# is seen. `-z` for the same reason as above.
diff_err=$(git diff --no-renames --name-only --diff-filter=A -z "$TRACKING" HEAD -- "${MIG_DIR}/" 2>&1 >"$ADDED_LIST")
diff_status=$?
if [ "$diff_status" -ne 0 ]; then
  fail_ref "\`git diff --no-renames --name-only --diff-filter=A -z ${TRACKING} HEAD -- ${MIG_DIR}/\` failed (exit ${diff_status}):
  $(printf '%s' "$diff_err" | sed 's/^/  /')" \
    "confirm HEAD and ${REMOTE}/${BRANCH} both resolve."
fi

if [ ! -s "$ADDED_LIST" ]; then
  # Nothing added, so nothing can collide. Exit without contacting the remote —
  # this is the offline path, and it is reached only after a *successful* diff
  # and only after the positive control above has confirmed MIG_DIR is real.
  exit 0
fi

# ── 2. Authoritative remote state — no fetch, no stdin ────────────────────
# Reached only when the push actually adds a migration. This is the one place
# the network is required, and nothing above it needs the network.
#
# Exit status is inspected before the output is, because `ls-remote` prints
# nothing on stdout both when the ref is absent and when it could not run at
# all. Empty output is not, on its own, an answer.
remote_out=$(git ls-remote "$REMOTE" "$REMOTE_REF" 2>&1)
ls_status=$?
if [ "$ls_status" -ne 0 ]; then
  fail_ref "\`git ls-remote ${REMOTE} ${REMOTE_REF}\` failed (exit ${ls_status}):
  $(printf '%s' "$remote_out" | sed 's/^/  /')" \
    "restore network access to ${REMOTE}, or check \`git remote -v\`."
fi

remote_sha=$(printf '%s\n' "$remote_out" | awk -v r="$REMOTE_REF" '$2 == r { print $1 }')
if [ -z "$remote_sha" ]; then
  fail_ref "${REMOTE} has no ${REMOTE_REF}." \
    "push ${BRANCH} to ${REMOTE} first, or point this guard at the branch that
          actually holds the migration history."
fi

# ── 2b. The tracking ref must match the remote ────────────────────────────
# We read the *tree* from `origin/main` because those objects are local. A stale
# tracking ref would silently compare against an old migration set — the
# fail-open this whole guard exists to prevent — so prove it first. That
# $TRACKING resolves at all was already established in section 1.
if [ "$tracking_sha" != "$remote_sha" ]; then
  fail_ref "${REMOTE}/${BRANCH} is stale.
      local  ${REMOTE}/${BRANCH} = ${tracking_sha}
      remote ${REMOTE_REF} = ${remote_sha}" \
    "git fetch ${REMOTE} ${BRANCH}"
fi

# ── 3. Parse the remote's migration set ───────────────────────────────────
# $REMOTE_LIST was read in section 1 from the local tracking ref; section 2b has
# now proven that ref equals the remote, so these numbers are authoritative.
remote_max=0
remote_nums=""
while IFS= read -r -d '' path; do
  [ -n "$path" ] || continue
  n=$(migration_number "$path")
  if [ -z "$n" ]; then
    reject_unparsable "$path" " on ${REMOTE}/${BRANCH}"
    exit 1
  fi
  remote_nums="${remote_nums}${n}"$'\n'
  if [ "$n" -gt "$remote_max" ]; then
    remote_max="$n"
  fi
done < "$REMOTE_LIST"

# ── 4. Assert both properties, plus intra-push uniqueness ─────────────────
# The third one is not redundant with the first: after parallel worktrees are
# merged sequentially into local `main` and pushed together, two files both
# claiming 148 are *both* absent from the remote and *both* above its highest.
# Checked against the remote alone, both pass. That is failure shape 1.
claimed=""       # numbers accepted so far in this push
# Parallel arrays, not a delimited string. An earlier version accumulated
# "path<TAB>number<TAB>reason" lines and re-read them with `IFS=$'\t' read`,
# which a path containing a literal newline splits into two records — producing
# a phantom rejection with an empty number in the report. The verdict was still
# correct; the message was not. Arrays hold arbitrary bytes, so there is no
# delimiter left to collide with. Iterated by index rather than "${a[@]}" so an
# empty array is safe under `set -u` on bash 3.2.
bad_paths=()
bad_nums=()
bad_reasons=()
ok=1

while IFS= read -r -d '' path; do
  [ -n "$path" ] || continue
  n=$(migration_number "$path")
  if [ -z "$n" ]; then
    reject_unparsable "$path" ""
    ok=0
    continue
  fi
  reason=""
  if printf '%s' "$remote_nums" | grep -qx -- "$n"; then
    reason="already on ${REMOTE}/${BRANCH}"
  elif [ "$n" -le "$remote_max" ]; then
    reason="at or below ${REMOTE}/${BRANCH}'s highest (${remote_max}) — a hole, not a free slot"
  elif printf '%s' "$claimed" | grep -qx -- "$n"; then
    reason="claimed twice inside this push"
  fi
  if [ -n "$reason" ]; then
    bad_paths+=("$path")
    bad_nums+=("$n")
    bad_reasons+=("$reason")
    ok=0
  else
    claimed="${claimed}${n}"$'\n'
  fi
done < "$ADDED_LIST"

if [ "$ok" -eq 1 ]; then
  exit 0
fi

# ── 5. Tell the author what to rename the file to ─────────────────────────
# The next free number is one past the highest, skipping anything a valid file
# in this same push already took. Never the lowest unused integer: holes are
# permanently burned.
# Sets $next_free_result. NOT a command substitution: it has to mutate
# $claimed in the caller's shell so two rejected files in one push do not both
# get told to use the same number.
next_free_result=""
next_free() {
  local candidate=$((remote_max + 1))
  while printf '%s' "$claimed" | grep -qx -- "$candidate"; do
    candidate=$((candidate + 1))
  done
  claimed="${claimed}${candidate}"$'\n'
  next_free_result="$candidate"
}

# A file that failed to parse has already printed its own message and has no
# number to report here. Printing the block below with an empty file list would
# read as a broken check rather than a rejected name.
if [ "${#bad_paths[@]}" -eq 0 ]; then
  exit 1
fi

echo "ERROR: migration number rejected — this is the failure class that stops the"
echo "       service from booting, not a bookkeeping nit."
echo
echo "  ${REMOTE}/${BRANCH}'s highest migration number: ${remote_max}"
echo
i=0
while [ "$i" -lt "${#bad_paths[@]}" ]; do
  path="${bad_paths[$i]}"
  n="${bad_nums[$i]}"
  reason="${bad_reasons[$i]}"
  next_free
  # Strip at the first underscore of the *basename*, not at "${n}_": $n has had
  # leading zeros stripped, so a "008_x.sql" file would not match "*/8_".
  base="${path##*/}"
  printf '  %s\n' "$path"
  echo "      used:   ${n}  (${reason})"
  echo "      use:    ${next_free_result}"
  printf '      rename: git mv %s %s\n' "$path" "${MIG_DIR}/${next_free_result}_${base#*_}"
  echo
  i=$((i + 1))
done
echo "  A migration numbered at or below a database's highest applied version is"
echo "  never applied as a back-fill. Under goose's default"
echo "  \`allowMissing=false\` it does one of two things, decided by how old the"
echo "  database is:"
echo "    * number not in that database's ledger -> hard error, the service"
echo "      does not boot;"
echo "    * number already in its ledger         -> silently skipped, goose"
echo "      reports up to date and the migration never runs."
echo "  The second is the quieter and the worse one. Holes are permanently"
echo "  burned: the next free number is one past the highest, never the lowest"
echo "  unused integer."
echo
echo "  Rule: a migration number must be strictly greater than the highest in"
echo "  use on the shared branch, and collide with none already there."
exit 1
