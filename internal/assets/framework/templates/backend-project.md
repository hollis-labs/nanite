# Backend Context — {PROJECT_NAME}

> Project-specific backend conventions. Loaded as agent context when working in this project.
> Lives at `<project>/.nanite/agents/backend.md`.

## Stack

- **Go version:** {from go.mod}
- **Module path:** {from go.mod}
- **Router:** {Chi / net/http / none}
- **Database:** {PostgreSQL via pgx / SQLite / BoltDB / none}
- **CLI framework:** {Cobra / none}
- **Config format:** {YAML / TOML / env vars}
- **Notable dependencies:** {list key deps from go.mod}

## Project Structure

```
{Describe the actual directory layout}
cmd/
├── {binary}/         # Main entrypoint
internal/
├── {domain}/         # Core domain logic
├── {api}/            # HTTP handlers and routes
├── {config}/         # Configuration loading
└── {store}/          # Database/persistence layer
```

## Package Inventory

> List the key packages and their responsibilities.

| Package | Location | Responsibility |
|---------|----------|----------------|
| {api} | `internal/api/` | HTTP handlers and route registration |
| {config} | `internal/config/` | YAML config parsing and validation |
| {store} | `internal/store/` | Database access layer |

## Patterns to Follow

### Error Handling
- {How errors are wrapped and propagated in this project}
- {Custom error types, if any}
- {Error response format for APIs}

### Configuration
- {Where config is loaded, how it flows to packages}
- {Config struct location and naming}

### HTTP Handlers (if applicable)
- {Handler signature patterns}
- {Middleware chain}
- {How routes are registered}

### Database Access (if applicable)
- {Query patterns — raw SQL vs query builder}
- {Migration tool and location}
- {Connection management}

### Testing
- {Test file conventions}
- {Fixture/helper patterns}
- {Integration test setup}

## Anti-Patterns to Avoid

- **Package cycles** — Don't create import cycles. If two packages need each other, extract a shared interface or merge them.
- **Fat handlers** — HTTP handlers should parse input, call domain logic, format output. No business logic in handlers.
- **Stringly-typed config** — Use typed config structs, not raw map[string]interface{}.
- **Untested exports** — Every exported function should have at least one test.
- {Project-specific anti-patterns discovered during audit}

## Reference Implementations

> Point the agent at canonical examples of well-structured code.

| Pattern | Reference File | Why it's good |
|---------|---------------|---------------|
| {Handler} | `internal/api/{example}.go` | {Clean request parsing, proper error responses} |
| {Domain logic} | `internal/{domain}/{example}.go` | {Clear interfaces, testable design} |
| {Test} | `internal/{domain}/{example}_test.go` | {Table-driven, good coverage} |

## Build & Run

- **Build:** `{make build / go build -o binary ./cmd/binary}`
- **Test:** `{make test / go test ./...}`
- **Lint:** `{make lint / go vet ./...}`
- **Run:** `{./binary / make run}`

## Notes

- {Anything else the backend agent should know about this project}
