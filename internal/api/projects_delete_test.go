package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20261001-0125: DELETE /api/projects/{pid} for a project that ever had a
// session was an opaque 500 (FOREIGN KEY constraint).
func TestDeleteProject_LiveSessionIs409ThenArchivedOnlyDeletes(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()
	st := a.store
	if err := st.CreateProject(ctx, &store.Project{ID: "proj-del", Name: "Delete me"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, id := range []string{"sess-del-live", "sess-del-old"} {
		if err := st.CreateSession(ctx, &store.Session{ID: id, Title: "title " + id, ProjectID: "proj-del"}); err != nil {
			t.Fatalf("CreateSession(%s): %v", id, err)
		}
	}
	if err := st.ArchiveSession(ctx, "sess-del-old"); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	del := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/projects/proj-del", nil))
		return w
	}

	w := del()
	if w.Code != http.StatusConflict {
		t.Fatalf("DELETE with a live session = %d %s, want 409", w.Code, w.Body.String())
	}
	var conflict struct {
		Error    string                    `json:"error"`
		Sessions []store.ProjectSessionRef `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode 409 body: %v (%s)", err, w.Body.String())
	}
	if !strings.Contains(conflict.Error, "not archived") || len(conflict.Sessions) != 1 || conflict.Sessions[0].ID != "sess-del-live" {
		t.Fatalf("409 body = %+v, want a clear message naming only the live session", conflict)
	}

	if err := st.ArchiveSession(ctx, "sess-del-live"); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if w := del(); w.Code != http.StatusOK {
		t.Fatalf("DELETE with only archived sessions = %d %s, want 200", w.Code, w.Body.String())
	}
	for _, id := range []string{"sess-del-live", "sess-del-old"} {
		sess, err := st.GetSession(ctx, id)
		if err != nil || sess.ProjectID != "" {
			t.Fatalf("session %s after delete = %+v, %v; want kept and detached", id, sess, err)
		}
	}
	if w := del(); w.Code != http.StatusNotFound {
		t.Fatalf("DELETE of the deleted project = %d, want 404", w.Code)
	}
}
