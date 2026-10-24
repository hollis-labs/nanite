package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A made-up secret in the shape of a real one. It is a fixture, not a credential.
const issueTestSecret = "nan_client_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // #nosec G101

type issueServer struct {
	url             string
	posts, revokes  atomic.Int32
	revokedID       atomic.Value
	authorizedToken string
}

func newIssueServer(t *testing.T) *issueServer {
	t.Helper()
	is := &issueServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && (r.URL.Path == "/api/auth/credentials" || strings.HasSuffix(r.URL.Path, "/rotate")):
			is.posts.Add(1)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"cred-1","kind":"client","label":"tachyon","scopes":["agents:manage"],"secret":"`+issueTestSecret+`"}`)
		case r.Method == http.MethodDelete:
			is.revokes.Add(1)
			is.revokedID.Store(strings.TrimPrefix(r.URL.Path, "/api/auth/credentials/"))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	is.url = srv.URL
	t.Setenv("NANITE_API_URL", srv.URL)
	t.Setenv("NANITE_AUTH_TOKEN", "test-only-operator-token")
	return is
}

// captureStderr returns what fn wrote to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

func TestAuthIssueWritesSecretOnlyToTheFile(t *testing.T) {
	newIssueServer(t)
	out := filepath.Join(t.TempDir(), "sub", "tachyon.token")
	var stdout bytes.Buffer
	var err error
	stderr := captureStderr(t, func() {
		err = authIssue(&stdout, http.MethodPost, "/api/auth/credentials", map[string]any{"kind": "client"}, out, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	data, rerr := os.ReadFile(out)
	if rerr != nil || strings.TrimSpace(string(data)) != issueTestSecret {
		t.Fatalf("file = %q, %v", data, rerr)
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", st.Mode().Perm())
	}
	for name, text := range map[string]string{"stdout": stdout.String(), "stderr": stderr} {
		if strings.Contains(text, issueTestSecret) || strings.Contains(text, "nan_client_") {
			t.Errorf("%s carries the secret: %s", name, text)
		}
	}
	if !strings.Contains(stdout.String(), `"id": "cred-1"`) || !strings.Contains(stdout.String(), out) {
		t.Errorf("stdout lacks the metadata and path: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), `"secret"`) {
		t.Errorf("stdout still has a secret field: %s", stdout.String())
	}
	if entries, _ := os.ReadDir(filepath.Dir(out)); len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestAuthIssueRefusesAnExistingFileBeforeIssuing(t *testing.T) {
	is := newIssueServer(t)
	out := filepath.Join(t.TempDir(), "tachyon.token")
	if err := os.WriteFile(out, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := authIssue(&stdout, http.MethodPost, "/api/auth/credentials", map[string]any{}, out, false)
	if err == nil || is.posts.Load() != 0 {
		t.Fatalf("err = %v, server minted %d credentials; want a refusal before any request", err, is.posts.Load())
	}
	if b, _ := os.ReadFile(out); string(b) != "keep me\n" {
		t.Fatalf("existing file changed: %q", b)
	}
	// --force replaces it.
	if err = authIssue(&stdout, http.MethodPost, "/api/auth/credentials", map[string]any{}, out, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); strings.TrimSpace(string(b)) != issueTestSecret {
		t.Fatalf("forced file = %q", b)
	}
}

// If the file cannot be written after the server minted, the new credential is
// revoked and the secret appears nowhere, not even in the error.
func TestAuthIssueRevokesWhenTheFileCannotBeWritten(t *testing.T) {
	is := newIssueServer(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(blocker, "tachyon.token") // parent is a file: cannot be created
	var stdout bytes.Buffer
	var err error
	stderr := captureStderr(t, func() {
		err = authIssue(&stdout, http.MethodPost, "/api/auth/credentials", map[string]any{}, out, false)
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if id, _ := is.revokedID.Load().(string); is.revokes.Load() != 1 || id != "cred-1" {
		t.Fatalf("revokes = %d id = %q, want cred-1 revoked", is.revokes.Load(), id)
	}
	for name, text := range map[string]string{"error": err.Error(), "stdout": stdout.String(), "stderr": stderr} {
		if strings.Contains(text, issueTestSecret) {
			t.Errorf("%s carries the secret: %s", name, text)
		}
	}
}

func TestAuthIssueErrorBodiesAreRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad `+issueTestSecret+`"}`)
	}))
	defer srv.Close()
	t.Setenv("NANITE_API_URL", srv.URL)
	t.Setenv("NANITE_AUTH_TOKEN", "test-only-operator-token")
	err := authIssue(io.Discard, http.MethodPost, "/api/auth/credentials", map[string]any{}, filepath.Join(t.TempDir(), "f"), false)
	if err == nil || strings.Contains(err.Error(), issueTestSecret) {
		t.Fatalf("err = %v", err)
	}
}
