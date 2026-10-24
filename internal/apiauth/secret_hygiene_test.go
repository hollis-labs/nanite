package apiauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Show-once: a credential's secret appears in the response that issued it and
// nowhere else. Run the whole lifecycle (create, use, wrong secret, rotate,
// revoke) with every log level captured and assert no secret reaches the logs,
// the audit table, any other table column, or the database file.
func TestSecretNeverReachesLogsAuditOrStorage(t *testing.T) {
	logs := &lockedBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.Level(-8)})))
	t.Cleanup(func() { slog.SetDefault(old) })

	h := newHarness(t)
	opCred, op := h.mint(KindOperator)
	_ = opCred
	resp, body := postJSON(h, "/api/auth/credentials", op, map[string]any{"kind": "client", "label": "tachyon", "scopes": []string{"read", "agents:manage"}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var created struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.Secret == "" {
		t.Fatalf("create body: %v %s", err, body)
	}
	secrets := []string{created.Secret, op}
	// Use it, abuse it, rotate it, revoke it.
	h.status("GET", "/api/sessions", created.Secret)
	h.status("POST", "/api/agent-setup", created.Secret)
	h.status("PUT", "/api/settings", created.Secret) // refused
	h.status("GET", "/api/sessions", created.Secret[:len(created.Secret)-2]+"xx")
	h.status("GET", "/api/sessions", "nan_client_"+strings.Repeat("A", 43))
	resp, body = postJSON(h, "/api/auth/credentials/"+created.ID+"/rotate", op, map[string]any{}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("rotate = %d %s", resp.StatusCode, body)
	}
	var rotated struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal([]byte(body), &rotated)
	if rotated.Secret == "" || rotated.Secret == created.Secret {
		t.Fatalf("rotate returned %s", body)
	}
	secrets = append(secrets, rotated.Secret)
	h.status("GET", "/api/sessions", rotated.Secret)
	h.do("DELETE", "/api/auth/credentials/"+rotated.ID, op, nil)
	h.status("GET", "/api/sessions", rotated.Secret) // revoked

	leak := func(where, text string) {
		t.Helper()
		for _, s := range secrets {
			if strings.Contains(text, s) {
				t.Errorf("%s contains a credential secret", where)
			}
		}
		// Also the random 43-character body of each secret, in case a prefix was cut.
		for _, s := range secrets {
			if i := strings.LastIndex(s, "_"); i >= 0 && strings.Contains(text, s[i+1:]) {
				t.Errorf("%s contains a credential secret body", where)
			}
		}
	}
	leak("logs", logs.String())

	rows, err := h.store.db.QueryContext(context.Background(), `SELECT * FROM auth_audit`)
	if err != nil {
		t.Fatal(err)
	}
	cols, _ := rows.Columns()
	var audit strings.Builder
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		_ = rows.Scan(ptrs...)
		for _, v := range vals {
			audit.WriteString(strings.TrimSpace(strings.ReplaceAll(toString(v), "\n", " ")) + "|")
		}
		audit.WriteString("\n")
	}
	_ = rows.Close()
	if audit.Len() == 0 {
		t.Fatal("expected audit rows")
	}
	leak("auth_audit", audit.String())

	// Stored form: hashes only, so no plaintext secret in any file of the database.
	var dbPath string
	_ = h.store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath)
	if dbPath == "" {
		t.Fatal("cannot locate the credential database")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(dbPath + suffix) // #nosec G304 -- the test's own temp database
		if err != nil {
			continue
		}
		leak("database file "+filepath.Base(dbPath)+suffix, string(data))
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func TestWriteSecretFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "t.token")
	if err := WriteSecretFile(path, "s3cret", false); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	if got, _ := ReadTokenFile(path); got != "s3cret" {
		t.Fatalf("read back %q", got)
	}
	err := WriteSecretFile(path, "other", false)
	if !errors.Is(err, ErrSecretFileExists) {
		t.Fatalf("overwrite without force = %v, want ErrSecretFileExists", err)
	}
	if got, _ := ReadTokenFile(path); got != "s3cret" {
		t.Fatalf("file was changed: %q", got)
	}
	if err = WriteSecretFile(path, "other", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadTokenFile(path); got != "other" {
		t.Fatalf("forced write = %q", got)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("stray files: %v", entries)
	}
	if err = WriteSecretFile(filepath.Join(path, "x"), "s3cret", false); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error = %v", err)
	}
}
