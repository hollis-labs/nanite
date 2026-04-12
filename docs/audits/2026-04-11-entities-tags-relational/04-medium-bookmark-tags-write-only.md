# [Medium] Bookmark tags are write-only with no update or query-by-tag support

**Scope:** internal/store/bookmarks.go, internal/api/bookmarks.go
**Topic:** Tag CRUD completeness
**Date:** 2026-04-11

## Problem

Bookmarks have a `tags TEXT DEFAULT '[]'` column in the schema and the `Tags` field in the Go struct, but:

1. **No update path.** There is no `UpdateBookmarkTags` method. `UpdateBookmarkNote` updates only the `note` field. Once created, a bookmark's tags are frozen.
2. **No query-by-tag.** There is no way to filter bookmarks by tag. `ListBookmarks` returns all bookmarks for a session.
3. **No API exposure for tags on create.** The `CreateBookmarkRequest` type (`internal/api/types.go:L187-191`) does not include a `Tags` field:

```go
type CreateBookmarkRequest struct {
    MessageID string `json:"message_id"`
    SessionID string `json:"session_id"`
    Note      string `json:"note"`
}
```

So even at creation time, the API cannot set tags on a bookmark. The store's `CreateBookmark` accepts tags, but the API handler never passes them through.

## Evidence

`internal/store/bookmarks.go:L45-63` -- CreateBookmark accepts `b.Tags`:

```go
func (s *Store) CreateBookmark(b *Bookmark) error {
    if b.Tags == "" {
        b.Tags = "[]"
    }
    ...
    _, err := s.DB.Exec(
        `INSERT INTO bookmarks (id, message_id, session_id, note, tags, created_at)
         VALUES (?, ?, ?, ?, ?, ?)`,
        b.ID, b.MessageID, b.SessionID, nullIfEmpty(b.Note), b.Tags, now,
    )
```

`internal/api/bookmarks.go` -- the handler creates a bookmark without tags:

```go
b := &store.Bookmark{
    MessageID: req.MessageID,
    SessionID: req.SessionID,
    Note:      req.Note,
}
```

No `Tags` field set. The store default kicks in (`"[]"`).

`internal/store/bookmarks.go:L89-96` -- UpdateBookmarkNote only updates `note`:

```go
func (s *Store) UpdateBookmarkNote(id, note string) error {
    _, err := s.DB.Exec(`UPDATE bookmarks SET note = ? WHERE id = ?`, note, id)
```

## Impact

The bookmark tags feature is structurally present (schema, struct) but functionally dead. No code path can write or query bookmark tags via the API. The column exists as dead weight.

## Recommendation

Either:
1. **Wire it up:** Add `Tags` to `CreateBookmarkRequest`, add `UpdateBookmarkTags` store method, add tag filter to `ListBookmarks`.
2. **Remove it:** If bookmark tags are not planned, drop the column to avoid confusion. This is a schema change.

The first option is preferred if the feature is intended for the UI's bookmark organization.

## References

- `internal/store/bookmarks.go` -- full file.
- `internal/api/bookmarks.go` -- bookmark API handlers.
- `internal/api/types.go:L187-191` -- CreateBookmarkRequest.
- Schema: `internal/store/migrations/001_schema.sql:L141-149`.
