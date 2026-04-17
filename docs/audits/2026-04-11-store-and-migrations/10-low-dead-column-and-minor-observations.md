# [Low] Dead columns, minor inconsistencies, and style observations

**Scope:** internal/store/
**Topic:** Antipatterns / Idioms
**Date:** 2026-04-11

This file groups small findings that don't warrant individual files.

---

## 10a. Dead column: `default_adapter` in user_settings

**Evidence:** `001_schema.sql:L320` defines `default_adapter TEXT NOT NULL DEFAULT ''` but no Go code reads or writes this column. `GetUserSettings` (user_settings.go:L28-81) and `UpdateUserSettings` (user_settings.go:L84-140) both skip it entirely.

**Impact:** Dead schema. A future developer might try to use it, not realizing it's never populated.

**Recommendation:** Either wire it into `GetUserSettings`/`UpdateUserSettings` or drop it in the next migration.

---

## 10b. `splitSQL` uses naive BEGIN/END counting

**Evidence:** `store.go:L127-149` -- the `splitSQL` function counts occurrences of "BEGIN" and "END" in uppercased SQL to track trigger nesting. This would miscount if a string literal or column name contained "BEGIN" or "END" (e.g., `description TEXT DEFAULT 'BEGIN here'`).

**Impact:** Low. All current migrations use `BEGIN`/`END` only for trigger definitions, and no string literals contain these keywords. But a future migration with a `DEFAULT 'BEGIN...'` value would be mis-split.

**Recommendation:** If trigger-containing migrations grow, consider a proper state-machine parser or use `--` separator comments between statements.

---

## 10c. `ListEvents` scans `created_at` as `time.Time` but stores as DATETIME default

**Evidence:** `events.go:L52-53` scans `created_at` into a `time.Time`:
```go
var ts time.Time
if err := rows.Scan(&e.ID, &e.SessionID, &e.EventType, &e.Category, &e.Detail, &e.Metadata, &ts); err != nil {
```

All other store methods scan `created_at` as `string`. The `event_log` table uses `DATETIME DEFAULT CURRENT_TIMESTAMP` which stores as text in SQLite. The `modernc.org/sqlite` driver may or may not parse this into `time.Time` correctly depending on the format.

**Impact:** Minor inconsistency. Works in practice because the driver handles both formats, but breaks the package's convention of string-typed timestamps everywhere else.

**Recommendation:** Scan as `string` for consistency, matching every other query in the package.

---

## 10d. `ListBrokerDecisions` returns `nil` slice instead of empty slice

**Evidence:** `broker.go:L47` -- `var decisions []BrokerDecision` initializes to nil. If no rows are returned, the function returns `nil, nil` instead of `[]BrokerDecision{}, nil`. API endpoints that JSON-serialize this will emit `null` instead of `[]`.

**Impact:** Frontend may need to null-check where it expects an array. Most list functions in the package use `make([]T, 0)` which serializes as `[]`. This is the only deviation.

**Recommendation:** Use `decisions := make([]BrokerDecision, 0)` for JSON consistency.

---

## 10e. `UpdateProvider` issues up to 3 separate UPDATE statements

**Evidence:** `providers.go:L103-130` -- `UpdateProvider` checks each field individually and issues a separate UPDATE for each non-nil field. This means up to 3 round-trips and 3 separate `updated_at` timestamps within the same logical update.

**Impact:** Minor. The timestamps will differ by microseconds. No correctness issue but slightly wasteful.

**Recommendation:** Build a single UPDATE with the fields that need changing, or accept the current pattern as "good enough" for the low update frequency.

## References

- All findings in this file are Low severity. They represent minor quality/consistency issues, not bugs or risks.
