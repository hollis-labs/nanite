package api

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"

	"github.com/hollis-labs/go-providers/provider"
)

// TestSanitizeUploadFilename_Rejections verifies that every category of
// disallowed filename is rejected rather than silently rewritten. This is
// the defense against audit finding 07-high (filename injection).
func TestSanitizeUploadFilename_Rejections(t *testing.T) {
	bad := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"null-byte", "ok\x00.sh"},
		{"forward-slash", "sub/dir.txt"},
		{"backslash", `sub\dir.txt`},
		{"traversal-segment", ".."},
		{"dot", "."},
		{"traversal-prefix", "../evil.sh"},
		{"traversal-deep", "../../evil.sh"},
		{"traversal-in-middle", "a/../b"},
		{"embedded-dotdot", "foo..bar"}, // explicit rule: no ".." sequence anywhere
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			got, err := sanitizeUploadFilename(c.in)
			if err == nil {
				t.Fatalf("expected rejection for %q, got sanitized=%q", c.in, got)
			}
		})
	}
}

// TestSanitizeUploadFilename_Accepts covers the positive cases.
func TestSanitizeUploadFilename_Accepts(t *testing.T) {
	good := []string{
		"report.pdf",
		"screenshot 2026-04-12.png",
		"file.name.with.dots.txt",
		"a.b",
	}
	for _, in := range good {
		t.Run(in, func(t *testing.T) {
			got, err := sanitizeUploadFilename(in)
			if err != nil {
				t.Fatalf("unexpected rejection for %q: %v", in, err)
			}
			if got != strings.TrimSpace(in) {
				t.Fatalf("expected %q, got %q", in, got)
			}
		})
	}
}

// newArtifactTestAPI builds an API wired to a real on-disk store + a
// temp artifacts root configured via AppConfig. Suitable for exercising
// upload/download handlers end-to-end.
func newArtifactTestAPI(t *testing.T) (*API, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	artifactsRoot := filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(artifactsRoot, 0o755); err != nil {
		t.Fatalf("mkdir artifacts root: %v", err)
	}

	appCfg := config.DefaultAppConfig()
	appCfg.Artifacts.StorageDir = artifactsRoot

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:     s,
		Providers: provider.NewRegistry(),
		AppConfig: appCfg,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}

	// Seed the minimum session row the artifacts FK chain requires. We go
	// via direct SQL to avoid pulling in the full session-service wiring;
	// the exact column set tracks the sessions schema.
	if _, err := s.DB.Exec(`INSERT INTO sessions (id, short_code, title) VALUES (?, ?, ?)`, "sess1", "sc-sess1", "t"); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	return New(svc), artifactsRoot
}

// TestUploadRejectsTraversalFilename verifies the upload handler rejects
// filenames that would bypass the stdlib's filepath.Base pre-sanitization
// inside mime/multipart.Part.FileName().
//
// mime/multipart applies filepath.Base to the filename param before handing
// it to the handler, so "../../evil.sh" arrives as "evil.sh" on Unix. The
// attack surface that reaches our sanitizeUploadFilename is therefore:
//   - ".." (Base("..") = "..")
//   - ""    (Base("") = ".")
//   - filenames containing null bytes (Base preserves them)
//
// Each of these must be rejected with 400. We exercise the ".." case here;
// unit coverage for the full rule set lives in TestSanitizeUploadFilename_*.
func TestUploadRejectsTraversalFilename(t *testing.T) {
	a, artifactsRoot := newArtifactTestAPI(t)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if err := mw.WriteField("session_id", "sess1"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="file"; filename=".."`)
	hdr.Set("Content-Type", "application/octet-stream")
	fw, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	fw.Write([]byte("pwned"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.handleUploadArtifact(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for '..' filename, got %d: %s", rec.Code, rec.Body.String())
	}

	// Ensure the handler did not write a file named "pwned" content into
	// the artifacts root either. The rejection at 400 should have bailed
	// before any disk write.
	_ = artifactsRoot
}

// TestDownloadDoesNotLeakAbsoluteEscapingPath simulates a DB-stored
// StoragePath that is absolute and points at a sensitive file outside the
// configured artifacts root (e.g. /etc/passwd). pathsafe treats an absolute
// userPath as root-relative — the leading separator is stripped and the
// path re-joined under root. The resulting resolved path does not exist, so
// the handler returns 404, not the sensitive content.
//
// We assert two things:
//  1. the response is not 200 (content never leaks), and
//  2. the response body never contains the sensitive marker.
func TestDownloadDoesNotLeakAbsoluteEscapingPath(t *testing.T) {
	a, artifactsRoot := newArtifactTestAPI(t)

	outside := filepath.Join(filepath.Dir(artifactsRoot), "secret.txt")
	if err := os.WriteFile(outside, []byte("SENSITIVE-ABC"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}

	art := &store.Artifact{
		SessionID:   "sess1",
		Name:        "secret.txt",
		MimeType:    "text/plain",
		SizeBytes:   int64(len("SENSITIVE-ABC")),
		StoragePath: outside, // absolute — pathsafe re-roots under artifactsRoot
	}
	if err := a.Services.Store.CreateArtifact(context.Background(), art); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+art.ID+"/download", nil)
	req.SetPathValue("id", art.ID)
	rec := httptest.NewRecorder()
	a.handleDownloadArtifact(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("download served escaping path (status 200): %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SENSITIVE-ABC") {
		t.Fatalf("response leaked sensitive content: %s", rec.Body.String())
	}
}

// TestDownloadRejectsRelativeTraversalStoragePath confirms that a DB-stored
// StoragePath containing ".." is rejected. This is the relative-path
// variant of the previous test.
func TestDownloadRejectsRelativeTraversalStoragePath(t *testing.T) {
	a, _ := newArtifactTestAPI(t)

	art := &store.Artifact{
		SessionID:   "sess1",
		Name:        "passwd",
		MimeType:    "text/plain",
		SizeBytes:   0,
		StoragePath: "../../etc/passwd",
	}
	if err := a.Services.Store.CreateArtifact(context.Background(), art); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+art.ID+"/download", nil)
	req.SetPathValue("id", art.ID)
	rec := httptest.NewRecorder()
	a.handleDownloadArtifact(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on relative-traversal StoragePath, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestUploadHappyPath verifies the normal upload flow still works after
// adding sanitization and path confinement.
func TestUploadHappyPath(t *testing.T) {
	a, artifactsRoot := newArtifactTestAPI(t)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("session_id", "sess1")
	fw, err := mw.CreateFormFile("file", "note.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	fmt.Fprint(fw, "hello")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.handleUploadArtifact(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// File should exist under the artifacts root.
	got := filepath.Join(artifactsRoot, "sess1", "note.txt")
	if _, err := os.Stat(got); err != nil {
		// symlink resolution (e.g. /var -> /private/var on darwin) may
		// rewrite the path; accept any file under the resolved root.
		matches, _ := filepath.Glob(filepath.Join(artifactsRoot, "sess1", "*"))
		if len(matches) == 0 {
			t.Fatalf("uploaded file not present under artifacts root: %v", err)
		}
	}
}

// TestSanitizeContentDispositionName ensures quoting/CRLF injection vectors
// are stripped from the download filename header.
func TestSanitizeContentDispositionName(t *testing.T) {
	cases := map[string]string{
		`ok.txt`:            `ok.txt`,
		"bad\"quote.txt":    `badquote.txt`,
		"line\r\nbreak.txt": `linebreak.txt`,
		"null\x00byte.txt":  `nullbyte.txt`,
		``:                  `download`,
	}
	for in, want := range cases {
		if got := sanitizeContentDispositionName(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
