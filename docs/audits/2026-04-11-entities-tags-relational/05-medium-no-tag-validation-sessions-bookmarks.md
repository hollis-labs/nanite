# [Medium] No tag validation for session and bookmark tags

**Scope:** internal/store/sessions.go, internal/service/chat_generate.go
**Topic:** Tag CRUD / Input validation
**Date:** 2026-04-11

## Problem

Agent tags are validated as a JSON string array during creation/update (`internal/agentvalidation/validation.go:L141-147`), but session and bookmark tags have no validation at all. Arbitrary strings can be written to the `tags` column.

## Evidence

**Agent tags -- validated** (`internal/agentvalidation/validation.go:L141-147`):

```go
// 8. Validate tags (JSON string array)
if t := strings.TrimSpace(agent.Tags); t != "" && t != "[]" {
    var tags []string
    if err := json.Unmarshal([]byte(t), &tags); err != nil {
        result.Errors = append(result.Errors, fmt.Sprintf("tags is malformed JSON array: %s", err.Error()))
    }
}
```

**Session tags -- no validation.** `UpdateSessionTags` (`internal/store/sessions.go:L179-189`) accepts raw `tagsJSON string` and writes it directly:

```go
func (s *Store) UpdateSessionTags(id, tagsJSON string) error {
    now := time.Now().UTC().Format(time.RFC3339)
    _, err := s.DB.Exec(
        `UPDATE sessions SET tags = ?, updated_at = ? WHERE id = ?`,
        tagsJSON, now, id,
    )
```

The `autoTags` caller in `chat_generate.go:L1116-1120` does validate implicitly by JSON-unmarshaling:

```go
var tags []string
if err := json.Unmarshal([]byte(raw), &tags); err != nil || len(tags) == 0 || len(tags) > 5 {
    return
}
tagsJSON, _ := json.Marshal(tags)
_ = s.store.UpdateSessionTags(sessionID, string(tagsJSON))
```

This is safe for the autoTags path, but `UpdateSessionTags` is also exposed in the service interface (`internal/service/store.go:L24`), so any future caller could write malformed JSON.

**Bookmark tags -- no validation.** `CreateBookmark` accepts `b.Tags` as a raw string with no JSON validation.

## Impact

- If a caller passes malformed JSON to `UpdateSessionTags`, the `tags` column will contain invalid JSON. Downstream consumers that `json.Unmarshal` the tags field will fail. The frontend tag display would break for that session.
- Currently the only session tag writer is `autoTags` (which validates), so this is a latent risk, not an active bug.
- Bookmark tags are currently dead (see finding 04), so the risk is theoretical.

## Recommendation

Add a `validateTagsJSON` helper in `internal/store/` (or `internal/agentvalidation/`) and call it in `UpdateSessionTags` before writing:

```go
func validateTagsJSON(tagsJSON string) error {
    var tags []string
    if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
        return fmt.Errorf("tags must be a JSON array of strings: %w", err)
    }
    return nil
}
```

## References

- `internal/agentvalidation/validation.go:L141-147` -- agent tag validation.
- `internal/store/sessions.go:L179-189` -- UpdateSessionTags.
- `internal/service/chat_generate.go:L1116-1120` -- autoTags caller.
- `internal/store/bookmarks.go:L45-63` -- CreateBookmark.
