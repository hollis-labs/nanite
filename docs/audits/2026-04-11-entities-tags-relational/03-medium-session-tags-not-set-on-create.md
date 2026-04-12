# [Medium] Session tags column not set during CreateSession

**Scope:** internal/store/sessions.go
**Topic:** Tag CRUD / Schema-code consistency
**Date:** 2026-04-11

## Problem

`CreateSession` does not include the `tags` column in its INSERT statement. The column defaults to `'[]'` via the schema, but the Go `Session` struct's `Tags` field remains empty string after creation (not `"[]"`). The only path that sets session tags is `autoTags()`, which fires asynchronously after a message exchange.

## Evidence

`internal/store/sessions.go:L142-154` -- the INSERT statement:

```go
_, err = s.DB.Exec(
    `INSERT INTO sessions (id, short_code, title, custom_name, workspace_id, project_id,
                           context_type, context_id, provider, model,
                           status, is_pinned, sort_order, message_count,
                           metadata, last_activity, created_at, updated_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
    ...
)
```

The `tags` column is not in this INSERT. The schema default (`DEFAULT '[]'`) fills it at the database level, so the DB row is correct. However, the returned `Session` struct has `Tags: ""` because the struct is never re-read after insert.

By contrast, `GetSession` and `ListSessions` read tags with `COALESCE(tags,'[]')`, so subsequent reads will correctly return `"[]"`. The inconsistency is only in the return value of `CreateSession` -- the calling code gets a `Session` with an empty `Tags` field until the next read.

`ForkSession` (`sessions.go:L481-549`) copies the source session's tags correctly:

```go
newSess := &Session{
    ...
    Tags:        src.Tags,
    ...
}
```

But since `CreateSession` doesn't write tags, the forked session's tags are also lost at creation time. They only appear because the source session's tags were read correctly.

## Impact

- API callers that use the `Session` returned by `CreateSession` will see an empty `Tags` field. The frontend will show no tags until a re-fetch.
- `ForkSession` preserves tags in the Go struct but doesn't actually write them to the new session's DB row (the `tags` column gets the schema default `'[]'`, not the source session's tags).
- This means forked sessions lose their source session's tags.

## Recommendation

Add `tags` to the CreateSession INSERT:

```go
`INSERT INTO sessions (..., tags, metadata, ...)
 VALUES (..., ?, ?, ...)`
```

And pass `sess.Tags` (defaulting to `"[]"` if empty). Also set `sess.Tags = "[]"` after creation so the returned struct is consistent.

## References

- `internal/store/sessions.go:L122-162` -- CreateSession.
- `internal/store/sessions.go:L481-549` -- ForkSession.
- `internal/service/chat_generate.go:L1080-1121` -- autoTags, the only writer.
