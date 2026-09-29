package tool

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const argumentsTableDDL = `CREATE TABLE tool_call_arguments (
	id TEXT PRIMARY KEY, session_id TEXT NOT NULL, tool_call_id TEXT NOT NULL, tool_name TEXT NOT NULL,
	created_at TEXT NOT NULL, expires_at TEXT NOT NULL, byte_size INTEGER NOT NULL, sha256 TEXT NOT NULL,
	was_truncated INTEGER NOT NULL DEFAULT 0, redacted_count INTEGER NOT NULL DEFAULT 0, body TEXT NOT NULL)`

func setupArgCache(t *testing.T, cfg ResultCacheConfig) (*ResultCache, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(argumentsTableDDL); err != nil {
		t.Fatal(err)
	}
	return NewResultCache(db, cfg), db
}

// dumpTable returns every stored column of every row as one string, so a test
// can assert a secret appears nowhere — not merely not in body.
func dumpTable(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT id, session_id, tool_call_id, tool_name, created_at, expires_at, byte_size, sha256, was_truncated, redacted_count, body FROM tool_call_arguments`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		cols := make([]any, 11)
		ptrs := make([]any, 11)
		for i := range cols {
			ptrs[i] = &cols[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&sb, cols...)
	}
	return sb.String()
}

func TestIsSecretKey(t *testing.T) {
	secret := []string{
		"password", "Password", "PASSWORD", "passwd", "passphrase", "db_password", "dbPassword",
		"secret", "client_secret", "clientSecret", "token", "access_token", "accessToken", "refresh-token",
		"api_key", "apiKey", "API-KEY", "api key", "x-api-key", "access_key", "private_key", "privateKey",
		"authorization", "Authorization", "auth_key", "cookie", "Set-Cookie", "bearer", "jwt", "id_jwt",
		"credentials", "aws_secret_access_key", "GITHUB_TOKEN", "signing_key", "encryption_key",
	}
	for _, k := range secret {
		if !IsSecretKey(k) {
			t.Errorf("IsSecretKey(%q) = false, want true", k)
		}
	}
	benign := []string{
		"path", "file_path", "query", "command", "content", "author", "authors", "mapping", "pinned",
		"footprint", "session_id", "max_tokens", "maxTokens", "input_tokens", "url", "name", "", "  ",
	}
	for _, k := range benign {
		if IsSecretKey(k) {
			t.Errorf("IsSecretKey(%q) = true, want false", k)
		}
	}
}

func TestNamePatternRedactor_Structure(t *testing.T) {
	in := map[string]any{
		"path":       "/tmp/x",
		"token":      "tok-AAA",
		"nested":     map[string]any{"password": "pw-BBB", "keep": "ok", "deeper": map[string]any{"api_key": "key-CCC"}},
		"list":       []any{map[string]any{"secret": "sec-DDD"}, "plain", 3.0},
		"headers":    map[string]any{"Authorization": "Bearer eyJhbGciOi.abc.def", "Accept": "json"},
		"object":     map[string]any{"a": 1.0}, // benign
		"cookie":     map[string]any{"session": "sess-EEE"},
		"nilsec":     nil,
		"max_tokens": 100.0,
	}
	out, n := NamePatternRedactor{}.Redact("t", in)
	raw, _ := json.Marshal(out)
	body := string(raw)
	for _, leaked := range []string{"tok-AAA", "pw-BBB", "key-CCC", "sec-DDD", "eyJhbGciOi", "sess-EEE"} {
		if strings.Contains(body, leaked) {
			t.Errorf("secret %q survived redaction: %s", leaked, body)
		}
	}
	for _, kept := range []string{"/tmp/x", `"keep":"ok"`, `"plain"`, `"Accept":"json"`, `"max_tokens":100`} {
		if !strings.Contains(body, kept) {
			t.Errorf("benign value %q was lost: %s", kept, body)
		}
	}
	if n != 6 {
		t.Errorf("redacted count = %d, want 6 (token, password, api_key, secret, Authorization, cookie)", n)
	}
	// Input is never mutated.
	if in["token"] != "tok-AAA" || in["nested"].(map[string]any)["password"] != "pw-BBB" {
		t.Error("redactor mutated its input")
	}
}

func TestNamePatternRedactor_SecretsInsideStrings(t *testing.T) {
	cases := []struct {
		name, in string
		leaks    []string
	}{
		{"curl bearer", `curl -H "Authorization: Bearer abcDEF123456789" https://x`, []string{"abcDEF123456789"}},
		{"bare bearer", `send Bearer sk-live-1234567890abcdef now`, []string{"sk-live-1234567890abcdef"}},
		{"env assign", `API_TOKEN=s3cr3tvalue go run .`, []string{"s3cr3tvalue"}},
		{"export quoted", `export DB_PASSWORD="hunter2 with spaces"`, []string{"hunter2", "with spaces"}},
		{"colon form", "client_secret: zzTOPsecret99\nother: fine", []string{"zzTOPsecret99"}},
		{"query string", `https://api.example.com/x?api_key=QQQ111&q=hi`, []string{"QQQ111"}},
		{"json as text", `{"password":"nestedPW","ok":1}`, []string{"nestedPW"}},
		{"json array as text", `[{"token":"nestedTOK"}]`, []string{"nestedTOK"}},
		{"basic auth", `Authorization: Basic dXNlcjpwYXNzd29yZA==`, []string{"dXNlcjpwYXNzd29yZA"}},
	}
	for _, tc := range cases {
		out, n := NamePatternRedactor{}.Redact("bash", map[string]any{"command": tc.in})
		body, _ := json.Marshal(out)
		if n == 0 {
			t.Errorf("%s: nothing redacted: %s", tc.name, body)
		}
		for _, leak := range tc.leaks {
			if strings.Contains(string(body), leak) {
				t.Errorf("%s: %q survived: %s", tc.name, leak, body)
			}
		}
	}
	// Ordinary text is untouched.
	out, n := NamePatternRedactor{}.Redact("bash", map[string]any{"command": "ls -la /tmp && echo done", "note": "the token count is fine"})
	if n != 0 {
		t.Errorf("benign strings redacted (%d): %v", n, out)
	}
}

func TestPersistArguments_RedactsBeforeAnyWrite(t *testing.T) {
	cache, db := setupArgCache(t, ResultCacheConfig{})
	rec, err := cache.PersistArguments("s1", "call-1", "http_request", map[string]any{
		"url":     "https://example.com",
		"headers": map[string]any{"Authorization": "Bearer supersecrettoken123"},
		"body":    `{"password":"hunter2hunter2"}`,
		"api_key": "AKIAEXAMPLEKEY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.RedactedCount != 3 {
		t.Errorf("RedactedCount = %d, want 3", rec.RedactedCount)
	}
	dump := dumpTable(t, db)
	for _, leaked := range []string{"supersecrettoken123", "hunter2hunter2", "AKIAEXAMPLEKEY"} {
		if strings.Contains(dump, leaked) {
			t.Errorf("secret %q reached the database: %s", leaked, dump)
		}
	}
	if !strings.Contains(dump, "https://example.com") || !strings.Contains(dump, RedactedPlaceholder) {
		t.Errorf("record lost its audit value: %s", dump)
	}
	got, err := cache.ListArguments("s1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListArguments = %v, %v", got, err)
	}
	if got[0].ToolCallID != "call-1" || got[0].ToolName != "http_request" || got[0].SHA256 == "" || got[0].RedactedCount != 3 {
		t.Errorf("record = %+v", got[0])
	}
}

func TestPersistArguments_HashIsOfRedactedDocument(t *testing.T) {
	cache, _ := setupArgCache(t, ResultCacheConfig{})
	a, _ := cache.PersistArguments("s", "1", "t", map[string]any{"path": "/x", "token": "one"})
	b, _ := cache.PersistArguments("s", "2", "t", map[string]any{"path": "/x", "token": "two"})
	c, _ := cache.PersistArguments("s", "3", "t", map[string]any{"path": "/y", "token": "one"})
	if a.SHA256 != b.SHA256 {
		t.Error("hash depends on the redacted secret value; low-entropy secrets would be guessable from it")
	}
	if a.SHA256 == c.SHA256 {
		t.Error("hash ignores non-secret argument differences")
	}
}

func TestPersistArguments_BudgetBoundsBodyKeepsHashAndSize(t *testing.T) {
	cache, db := setupArgCache(t, ResultCacheConfig{ArgumentBudgetBytes: 512})
	big := strings.Repeat("héllo wörld ", 2000) // multi-byte, well over budget
	rec, err := cache.PersistArguments("s", "1", "write_file", map[string]any{"path": "/a/b", "content": big, "password": "pw-LEAK"})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.WasTruncated || len(rec.Body) > 512+200 {
		t.Errorf("body not bounded: truncated=%v len=%d", rec.WasTruncated, len(rec.Body))
	}
	if rec.ByteSize <= 512 || rec.SHA256 == "" {
		t.Errorf("size/hash of the full document lost: %+v", rec)
	}
	if strings.ContainsRune(rec.Body, '�') {
		t.Error("truncation split a UTF-8 sequence")
	}
	if strings.Contains(dumpTable(t, db), "pw-LEAK") {
		t.Error("secret reached the database")
	}
}

func TestPersistArguments_RedactionRunsBeforeTruncation(t *testing.T) {
	// A secret past the preview window must still never be stored, and a secret
	// inside it must not survive because the budget cut happened first.
	cache, db := setupArgCache(t, ResultCacheConfig{ArgumentBudgetBytes: 300})
	args := map[string]any{"a_first": "x", "token": "EARLY-SECRET", "filler": strings.Repeat("z", 5000), "z_last_password": "LATE-SECRET"}
	if _, err := cache.PersistArguments("s", "1", "t", args); err != nil {
		t.Fatal(err)
	}
	dump := dumpTable(t, db)
	for _, s := range []string{"EARLY-SECRET", "LATE-SECRET"} {
		if strings.Contains(dump, s) {
			t.Errorf("%s stored", s)
		}
	}
}

func TestPersistArguments_TypedValuesAreNormalizedBeforeRedaction(t *testing.T) {
	// Input maps built in Go can hold typed values; redaction must see them as
	// the JSON the model sent, or a secret inside would slip past the walk.
	type creds struct {
		Password string `json:"password"`
		User     string `json:"user"`
	}
	cache, db := setupArgCache(t, ResultCacheConfig{})
	_, err := cache.PersistArguments("s", "1", "t", map[string]any{
		"login":   creds{Password: "typed-PW", User: "bob"},
		"env":     map[string]string{"API_TOKEN": "typed-TOK"},
		"raw":     json.RawMessage(`{"secret":"typed-SEC"}`),
		"strings": []string{"password=typed-PW2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dump := dumpTable(t, db)
	for _, s := range []string{"typed-PW", "typed-TOK", "typed-SEC", "typed-PW2"} {
		if strings.Contains(dump, s) {
			t.Errorf("typed secret %q stored: %s", s, dump)
		}
	}
	if !strings.Contains(dump, "bob") {
		t.Error("non-secret typed value lost")
	}
}

func TestPersistArguments_UnencodableArgumentsFailClosed(t *testing.T) {
	cache, db := setupArgCache(t, ResultCacheConfig{})
	if _, err := cache.PersistArguments("s", "1", "t", map[string]any{"ch": make(chan int), "token": "T"}); err == nil {
		t.Fatal("expected an error for unencodable arguments")
	}
	if dumpTable(t, db) != "" {
		t.Error("a row was written despite the error")
	}
}

func TestPersistArguments_NilAndEmptyArguments(t *testing.T) {
	cache, _ := setupArgCache(t, ResultCacheConfig{})
	rec, err := cache.PersistArguments("s", "1", "t", nil)
	if err != nil || rec.Body != "{}" {
		t.Errorf("nil args: body=%q err=%v", rec.Body, err)
	}
	if _, err := NewResultCache(nil, ResultCacheConfig{}).PersistArguments("s", "1", "t", nil); err == nil {
		t.Error("nil database must be an error, not a panic")
	}
}

type markerRedactor struct{}

func (markerRedactor) Redact(_ string, args map[string]any) (map[string]any, int) {
	return map[string]any{"dev": "policy"}, 1
}

func TestPersistArguments_RedactorIsReplaceable(t *testing.T) {
	cache, db := setupArgCache(t, ResultCacheConfig{ArgumentRedactor: markerRedactor{}})
	if _, err := cache.PersistArguments("s", "1", "t", map[string]any{"token": "x"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dumpTable(t, db), `{"dev":"policy"}`) {
		t.Error("configured redactor was not used")
	}
}

func TestPurge_RemovesExpiredArguments(t *testing.T) {
	cache, db := setupArgCache(t, ResultCacheConfig{})
	if _, err := db.Exec(`CREATE TABLE tool_result_cache (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, tool_name TEXT NOT NULL, tool_call_id TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, byte_size INTEGER NOT NULL, was_truncated INTEGER NOT NULL DEFAULT 0, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.PersistArguments("s", "live", "t", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tool_call_arguments VALUES ('old','s','x','t','2000-01-01T00:00:00Z','2000-01-02T00:00:00Z',2,'h',0,0,'{}')`); err != nil {
		t.Fatal(err)
	}
	if n, err := cache.Purge(); err != nil || n != 1 {
		t.Fatalf("Purge = %d, %v; want 1", n, err)
	}
	got, _ := cache.ListArguments("s")
	if len(got) != 1 || got[0].ToolCallID != "live" {
		t.Errorf("wrong rows survived: %+v", got)
	}
}
