# [Medium] SQLite database and Conduit store have no encryption at rest

**Scope:** Data at rest
**Topic:** Local data storage
**Date:** 2026-04-11

## Problem

The SQLite database (`nanite.db`) and Conduit data directory (`~/.conduit/`) store all conversation history, extracted memories, embedding vectors, session metadata, usage data, and tool results in plaintext. There is no encryption at rest. API keys are stored in the OS keychain (good), but everything else is accessible to any process running as the same user.

## Evidence

### nanite.db — plaintext SQLite

`internal/store/store.go:L30-42`:
```go
func New(dbPath string) (*Store, error) {
    absPath, err := filepath.Abs(dbPath)
    // ...
    db, err := sql.Open("sqlite", dbPath)
    // ...
    // Enable WAL mode and foreign keys.
    for _, pragma := range []string{
        "PRAGMA journal_mode=WAL",
        "PRAGMA foreign_keys=ON",
        "PRAGMA busy_timeout=5000",
    }
```

Standard SQLite open. No encryption pragma. No SQLCipher or equivalent.

The database contains (from schema migrations and store files):
- `sessions` — session metadata, agent assignments
- `messages` — full conversation content (user messages and assistant responses)
- `usage_log` — token counts, model names, cost data per message
- `artifacts` — file metadata created during sessions
- `mcp_servers` — MCP server URLs and connection configs
- `user_settings` — user preferences including utility model selection
- `plugin_settings` — plugin configuration values
- `provider_configs` — provider endpoint URLs (API keys are NOT stored here — they're in the OS keychain)

### Conduit store — plaintext SQLite

`~/.conduit/` contains a SQLite database with:
- Context records (project context, conversation context)
- Memory records (extracted memories with summaries and bodies)
- Embedding vectors (float32 blobs)

### API keys — properly secured

`internal/secrets/keyring.go:L16-20`:
```go
func Set(key, value string) error {
    if err := keyring.Set(serviceName, key, value); err != nil {
        return fmt.Errorf("keyring set %q: %w", key, err)
    }
    return nil
}
```

API keys use the OS keychain (macOS Keychain, Windows Credential Manager, Linux Secret Service). This is the correct approach.

### No `DELETE` or purge mechanism for user data

There is no `nanite purge` or `nanite delete-data` command. Users can manually delete `nanite.db` and `~/.conduit/`, but there is no guided data deletion workflow.

## Impact

- Any process running as the same OS user can read the full conversation history, extracted memories, and usage data.
- If the user's machine is compromised (malware, stolen laptop without full-disk encryption), all conversation data is accessible.
- This is standard for local-first SQLite applications. The mitigation is OS-level disk encryption (FileVault, BitLocker, LUKS). However, the absence of a deletion mechanism means users cannot easily exercise data removal.

## Recommendation

1. Document what data is stored and where: `nanite.db` (conversations, usage), `~/.conduit/` (memories, embeddings).
2. Add a `nanite data purge` command that deletes the SQLite database and optionally the Conduit directory, with confirmation.
3. Consider offering SQLCipher as an optional build flag for users who need application-level encryption. This is a significant effort and may not be warranted for a local-first tool.
4. Document that OS-level disk encryption (FileVault/BitLocker/LUKS) is the recommended protection for data at rest.

## References

- `internal/store/store.go:L30-50`
- `internal/secrets/keyring.go:L1-48`
- SQLite schema: `internal/store/migrations/`
