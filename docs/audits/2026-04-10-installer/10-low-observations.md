# [Low] Roundup of smaller installer observations

**Scope:** installer / multiple
**Topic:** Correctness, idioms, error handling, UX — smaller items
**Date:** 2026-04-10

A grouped set of smaller findings that don't individually warrant a file. Each is evidence-backed and fixable in isolation.

---

## L1: `cmdInstall` ignores `fs.Parse` errors via `flag.ExitOnError`

**File:** `cmd/nanite/install_cmd.go:L21-34`

`flag.NewFlagSet("install", flag.ExitOnError)` is called with `ExitOnError`, which causes `fs.Parse` to call `os.Exit(2)` directly on any unknown flag rather than returning an error. That's fine for simple commands, but the install command has several mutually-exclusive flag combinations that are checked *after* parsing (e.g., `--adapters` and `--no-adapters`). An unknown flag skips the conflict check entirely, which isn't wrong but is asymmetric with how the rest of the command handles bad input.

Not worth changing if the rest of the `cmd/nanite/*_cmd.go` files follow the same pattern — consistency with the project's CLI style wins. Listed as an observation.

---

## L2: `fmt.Scanln(&choice)` in `handlePartialInteractive` can't handle empty input

**File:** `cmd/nanite/install_cmd.go:L201-236`

```go
var choice string
fmt.Scanln(&choice)
```

`fmt.Scanln` returns an error if the user just hits enter with no input — and the code ignores that error. The subsequent `switch strings.ToLower(choice)` falls through to the `default:` case and prints "cancelled", which is probably the intended behavior for an empty line, but it happens via an error-swallow path rather than explicitly.

Recommendation: use `bufio.NewReader(os.Stdin).ReadString('\n')` (like `promptAdapterSelection` does) for consistency. This also makes the prompt testable in the same way the adapter prompt is.

---

## L3: `moveIfExists` on `os.Rename` silently overwrites

**File:** `internal/service/install/archive.go:L107-120`

```go
func moveIfExists(src, dst string) error {
    _, err := os.Stat(src)
    if errors.Is(err, fs.ErrNotExist) {
        return nil
    }
    ...
    if err := os.Rename(src, dst); err != nil {
        return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
    }
    return nil
}
```

`os.Rename` on Unix silently replaces a regular-file dst. The archive dir is newly created by `ResolveArchiveDir` + `os.MkdirAll`, so a collision in the happy path is rare — but if the user ran `nanite install --migrate-from-agentrc` with a `NANITE_ARCHIVE_BASE` that happens to have a pre-existing `.agentrc/` at the resolved path, the move will replace it. See finding `09-medium-archive-base-path-escape-via-env-var.md` for the full version of this concern.

Recommendation: `os.Stat(dst); if exists then error`. Combined with the env-var normalization in finding 09, this makes the archive flow fail-closed.

---

## L4: `handlePartialInteractive` prints to stdout, `installDie` prints to stderr — inconsistent

**File:** `cmd/nanite/install_cmd.go:L202-236`

The partial-install prompt prints `"How do you want to proceed?"` etc. to `os.Stdout` (via `fmt.Println` without an explicit writer), but `installDie` writes to `os.Stderr`. The mix of writers makes scripting the install awkward: a caller redirecting stdout loses the prompt but keeps the errors; a caller redirecting stderr gets the reverse.

Recommendation: use `os.Stderr` for the prompt (interactive prompts traditionally go to stderr so they don't pollute piped stdout output).

---

## L5: `isStdinTTY` check for interactivity drives branch logic but doesn't cover stdout

**File:** `cmd/nanite/install_cmd.go:L247-249`

```go
func isStdinTTY() bool {
    return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}
```

Stdin is the right signal for "can I prompt the user?" but the installer also prints banner output to stdout. If stdin is a TTY and stdout is a pipe (e.g., `nanite install | tee log.txt` then piped), the prompts would appear to hang because the user doesn't see the prompt line. This is the standard TTY-driven prompt trap.

Recommendation: either document that `nanite install` doesn't support that pipe pattern, or check both stdin AND stderr as the "where do we talk to the user" surface, using stderr for prompts. Matches the finding above.

---

## L6: `promptAdapterSelection` falls back silently after 3 invalid inputs

**File:** `internal/service/install/adapter_prompt.go:L104-132`

```go
for attempt := 0; attempt < 3; attempt++ {
    ...
    if perr != nil {
        fmt.Fprintf(w, "invalid input: %v\n", perr)
        continue
    }
    return picked, nil
}

// Fallback after 3 invalid attempts.
fmt.Fprintln(w, "too many invalid attempts; using fallback")
if isReconfigure {
    return current, nil
}
return []string{}, nil
```

After 3 bad inputs, the fresh-install path silently resolves to `[]` (no adapters). A beta user who is typing the wrong thing three times in a row is more likely confused than trying to select `[]` — the silent fallback may leave them with no adapters without them realizing it.

Recommendation: after fallback, print the resolved list clearly (`"using: claude, codex"` or `"using: no adapters"`) so the user knows what they got. This is a 1-line addition.

---

## L7: `installDie` formatter doesn't distinguish wrapped errors from the action

**File:** `cmd/nanite/install_cmd.go:L239-242`

```go
func installDie(action string, err error) {
    fmt.Fprintf(os.Stderr, "%s: %s: %v\n", brand.BinaryName, action, err)
    os.Exit(1)
}
```

With `fmt.Errorf("resolve adapters: %w", err)`, the user sees:

```
nanite: install project: resolve adapters: no such file
```

Two "action" prefixes (`install project:` and `resolve adapters:`) make it look like there are two layers of failure, but they're from the same call. Not wrong, just noisy.

Recommendation: either use `%+v` with a wrapped-error renderer, or strip the action prefix from the wrapped error before formatting. Low priority.

---

## L8: `setDifference` in `install.go` is package-private but test-visible only via black-box

**File:** `internal/service/install/install.go:L227-239`

No test. It's a 10-line helper with a trivial set-diff, and the integration tests exercise it indirectly, but there's no direct unit test. Low priority.

---

## L9: `InstallProjectReport.Warnings` is declared but never populated

**File:** `internal/service/install/install.go:L88-99`

```go
type InstallProjectReport struct {
    FreshScaffold      bool
    Migrated           bool
    Adopted            bool
    ArchiveOnly        bool
    ArchivePath        string
    Warnings []string
    ...
}
```

`Warnings []string` is on the struct but nothing ever appends to it. The install command never reads it. Dead field.

Recommendation: either wire up the carryover, cleanup, and symlink-existence warnings into this slice and print them in the post-install summary, or remove the field. Wiring it up is the user-friendly option, especially for the "broken symlink found during adopt" case that finding `04-high-scaffold-symlinks-created-without-target-validation.md` would produce.

---

## L10: `copyDir` uses `filepath.Walk` instead of `fs.WalkDir`

**File:** `internal/service/install/migrate.go:L228-253`

`filepath.Walk` is the older API; `filepath.WalkDir` (Go 1.16+) is faster and more idiomatic for directory traversal. The installer module uses Go 1.26 (per `reviewer-backend.md`). Not a bug, just style.

---

## L11: Install home tree is under-tested

**File:** `internal/service/install/install_test.go:L1-93`

`InstallHome` only has a handful of test cases. The "user-modified file preservation" contract is central — it's what `--force` overrides — but there's no test that writes a modified file, runs install without `--force`, and asserts the file is preserved byte-for-byte. There is one that asserts `SkippedFiles` is populated, which is adjacent but not the same.

Recommendation: add a round-trip test: extract, modify a file, re-extract without `--force`, assert modification is preserved; re-extract with `--force`, assert modification is overwritten.

---

## L12: `persistAdapterList` error message on non-document root

**File:** `internal/service/install/adapter_persist.go:L56-62`

```go
if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
    return fmt.Errorf("config %s is not a yaml document", path)
}
```

The error "is not a yaml document" is cryptic. If the user has an empty config.yaml or a yaml file containing only a scalar, they get this message with no hint of what to change. Add a line like "expected a top-level mapping" and maybe a reference to running `nanite install --refresh` to regenerate a default.

---

## L13: `ResolveOpts.Stdin`/`Stdout` in non-interactive path are never used but are part of the interface

**File:** `internal/service/install/adapter_select.go:L12-20`

Clean up: add a comment to `ResolveOpts` noting that Stdin/Stdout are only read when `Interactive=true`. Low priority documentation fix.
