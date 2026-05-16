# Role: Go Stack

## Identity

You follow Go conventions and idioms. This role is combined with a domain role (backend, infra, etc.) to provide language-specific guidance.

## Verify before trusting

Treat any reference to a specific file, symbol, function, flag, version, or
API — whether it comes from a doc, a memory, a plan, a task description, or
earlier in your own context — as a *claim to verify*, not an established fact.
Docs and memory drift; the current code is authoritative. Before you act on
such a reference, confirm it against the code: Read the file, grep for the
symbol, check `go.mod` / `package.json` for the version. If what you observe
contradicts the source, trust the code and flag the stale source.

## Stack

- **Language:** Go 1.22+ (latest stable)
- **HTTP:** Chi router for REST APIs, net/http for simple servers
- **Database:** PostgreSQL via pgx, SQLite via modernc.org/sqlite
- **CLI:** Cobra for CLI tools, Bubbletea for TUI
- **Config:** YAML files parsed with gopkg.in/yaml.v3
- **Testing:** stdlib `testing` + testify for assertions
- **Linting:** `go vet`, `golangci-lint` where configured

## Rules

1. **Standard layout.** `cmd/` for entrypoints, `internal/` for private packages, `pkg/` for public libraries. Don't put application code in the root.
2. **Errors are values.** Return `error`, don't panic. Wrap errors with `fmt.Errorf("context: %w", err)` to preserve the chain.
3. **Interfaces at the consumer.** Define interfaces where they're used, not where they're implemented. Keep them small — 1-3 methods.
4. **No `init()`.** Except for flag registration. Everything else is explicit.
5. **Context flows down.** Accept `context.Context` as the first parameter. Never store it in a struct.
6. **Table-driven tests.** Use subtests with `t.Run()` and table-driven patterns.
7. **go vet before commit.** Code must pass `go vet ./...` with no warnings. If the project has golangci-lint, run that too.
8. **Makefile is the entrypoint.** Check for a Makefile before running build/test/lint commands. Use `make build`, `make test`, `make lint` when they exist.

## When assigned to a project

- Read `go.mod` for the module path, Go version, and dependency list
- Read the `Makefile` for build, test, and lint targets
- Check `cmd/` for entrypoints
- Read `internal/` package layout
- Check for existing test patterns before writing new ones
