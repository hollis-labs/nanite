package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var artifactViewKeys = []string{
	"id", "session_id", "message_id", "name", "mime_type", "size_bytes", "storage_path",
	"metadata", "origin", "source_tool_call_id", "source_agent_id", "source_plugin_id", "created_at",
}

func TestArtifactViewJSON(t *testing.T) {
	var a store.Artifact
	populate(t, &a)
	assertKeys(t, "ArtifactView", mustJSON(t, artifactToView(&a)), artifactViewKeys)
	assertSameJSON(t, "populated", artifactToView(&a), a)
	assertSameJSON(t, "zero (source_* omitted)", artifactToView(&store.Artifact{}), store.Artifact{})
	assertSameJSON(t, "list", artifactsToView([]store.Artifact{a, {}}), []store.Artifact{a, {}})
	// The handlers always answered [] for no artifacts.
	assertSameJSON(t, "nil list", artifactsToView(nil), []store.Artifact{})
}

func artifactDo(a *testAPI, handler func(http.ResponseWriter, *http.Request), method, path, body string, pathValues map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// Every artifact error class keeps its status and body.
func TestArtifacts_ErrorClasses(t *testing.T) {
	a, root := newArtifactTestAPI(t)
	ctx := context.Background()

	// A row whose file is gone. (Stored relative: an absolute path to a
	// missing file under a symlinked root, such as macOS's /var temp dirs,
	// resolves as outside the root and is a 400 — the confinement rule's
	// existing behavior, not exercised here.)
	gone := &store.Artifact{SessionID: "sess1", Name: "gone.txt", MimeType: "text/plain", StoragePath: filepath.Join("sess1", "gone.txt")}
	if err := a.store.CreateArtifact(ctx, gone); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sess1", "adir"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	for _, c := range []struct {
		name string
		rec  *httptest.ResponseRecorder
		code int
		msg  string
	}{
		{"download unknown", artifactDo(a, a.handleDownloadArtifact, "GET", "/", "", map[string]string{"id": "nope"}), 404, "artifact not found"},
		{"download missing file", artifactDo(a, a.handleDownloadArtifact, "GET", "/", "", map[string]string{"id": gone.ID}), 404, "file not found on disk"},
		{"place missing field", artifactDo(a, a.handlePlaceArtifact, "POST", "/", `{"session_id":"sess1","name":"x"}`, nil), 400, "session_id, name, and storage_path are required"},
		{"place missing file", artifactDo(a, a.handlePlaceArtifact, "POST", "/", `{"session_id":"sess1","name":"x","storage_path":"sess1/none.txt"}`, nil), 400, ""},
		{"place directory", artifactDo(a, a.handlePlaceArtifact, "POST", "/", `{"session_id":"sess1","name":"x","storage_path":"sess1/adir"}`, nil), 400, "storage_path must name a regular file"},
	} {
		if c.rec.Code != c.code {
			t.Fatalf("%s: status %d, want %d: %s", c.name, c.rec.Code, c.code, c.rec.Body.String())
		}
		got := errorBody(t, c.rec)
		if c.msg != "" && got != c.msg {
			t.Fatalf("%s: error %q, want %q", c.name, got, c.msg)
		}
		if c.name == "place missing file" && !strings.HasPrefix(got, "storage_path must name an existing file: ") {
			t.Fatalf("%s: error %q", c.name, got)
		}
	}

	// A bad session id on upload.
	rec := httptest.NewRecorder()
	a.handleUploadArtifact(rec, artifactUploadRequest(t, "..", "note.txt", "x"))
	if rec.Code != http.StatusBadRequest || !strings.HasPrefix(errorBody(t, rec), "invalid session_id: ") {
		t.Fatalf("upload bad session: %d %s", rec.Code, rec.Body.String())
	}
}

// Place defaults the MIME type from the name and records the file's size;
// upload detects the MIME type when the declared one is generic.
func TestArtifacts_PlaceAndUploadRecordTypeAndSize(t *testing.T) {
	a, root := newArtifactTestAPI(t)
	if err := os.MkdirAll(filepath.Join(root, "sess1"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sess1", "r.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rec := artifactDo(a, a.handlePlaceArtifact, "POST", "/", `{"session_id":"sess1","name":"r.txt","storage_path":"sess1/r.txt","agent_id":"ag"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("place: %d %s", rec.Code, rec.Body.String())
	}
	var placed ArtifactView
	if err := json.Unmarshal(rec.Body.Bytes(), &placed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(placed.MimeType, "text/plain") || placed.SizeBytes != 5 || placed.Origin != string(store.ArtifactOriginPlaced) || placed.SourceAgentID != "ag" {
		t.Fatalf("placed = %+v", placed)
	}

	rec = httptest.NewRecorder()
	a.handleUploadArtifact(rec, artifactUploadRequest(t, "sess1", "page.html", "<p>hi</p>"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var uploaded ArtifactView
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(uploaded.MimeType, "text/html") || uploaded.SizeBytes != int64(len("<p>hi</p>")) || uploaded.Name != "page.html" {
		t.Fatalf("uploaded = %+v", uploaded)
	}

	// Both show up in the session list, and the origin filter narrows it.
	rec = artifactDo(a, a.handleListArtifactsByOrigin, "GET", "/?origin=placed", "", map[string]string{"id": "sess1"})
	var list []ArtifactView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].ID != placed.ID {
		t.Fatalf("by origin: %s (%v)", rec.Body.String(), err)
	}
	rec = artifactDo(a, a.handleListArtifactsByOrigin, "GET", "/", "", map[string]string{"id": "sess1"})
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("all: %s (%v)", rec.Body.String(), err)
	}
	if rec := artifactDo(a, a.handleListArtifactsByProject, "GET", "/", "", map[string]string{"id": "no-project"}); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("by project, none: %s", rec.Body.String())
	}
}

// The upload's own filename rule answers first, before path confinement
// would catch the same name.
func TestArtifacts_UploadFilenameRuleAnswers(t *testing.T) {
	a, _ := newArtifactTestAPI(t)
	rec := httptest.NewRecorder()
	a.handleUploadArtifact(rec, artifactUploadRequest(t, "sess1", "..", "x"))
	if rec.Code != http.StatusBadRequest || !strings.HasPrefix(errorBody(t, rec), "invalid filename: ") {
		t.Fatalf("upload '..': %d %s", rec.Code, rec.Body.String())
	}
}

// A stored name with quotes or line breaks cannot break out of the
// Content-Disposition header on download.
func TestArtifacts_DownloadSanitizesContentDisposition(t *testing.T) {
	a, root := newArtifactTestAPI(t)
	if err := os.MkdirAll(filepath.Join(root, "sess1"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sess1", "f.txt"), []byte("body"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	art := &store.Artifact{SessionID: "sess1", Name: "a\"b\r\nX-Evil: 1.txt", MimeType: "text/plain", StoragePath: filepath.Join("sess1", "f.txt")}
	if err := a.store.CreateArtifact(context.Background(), art); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	rec := artifactDo(a, a.handleDownloadArtifact, "GET", "/", "", map[string]string{"id": art.ID})
	if rec.Code != http.StatusOK || rec.Body.String() != "body" {
		t.Fatalf("download: %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="abX-Evil: 1.txt"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
}
