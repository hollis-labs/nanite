package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestBookmarkViewJSONKeys pins BookmarkView to the key set store.Bookmark
// emitted when it was serialized directly.
func TestBookmarkViewJSONKeys(t *testing.T) {
	want := []string{"created_at", "id", "message_id", "note", "session_id", "tags"}
	if got := jsonKeys(t, bookmarkToView(&store.Bookmark{})); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v\nwant   %v", got, want)
	}
	raw, _ := json.Marshal(bookmarksToView(nil))
	if string(raw) != "[]" {
		t.Fatalf("empty list marshaled %s, want []", raw)
	}
}

func seedBookmarkMessage(t *testing.T, a *testAPI) (*store.Session, *store.Message) {
	t.Helper()
	ctx := context.Background()
	sess := &store.Session{}
	if err := a.store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	msg := &store.Message{SessionID: sess.ID, Role: "user", Content: "remember this"}
	if err := a.store.CreateMessage(ctx, msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	return sess, msg
}

func doBookmarkRequest(mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// TestBookmarksAPI_ToggleCreateListRemove covers the toggle round trip and
// the list, with the response shapes the UI reads.
func TestBookmarksAPI_ToggleCreateListRemove(t *testing.T) {
	a, mux := newTestAPI(t)
	sess, msg := seedBookmarkMessage(t, a)

	if w := doBookmarkRequest(mux, "GET", "/api/sessions/"+sess.ID+"/bookmarks", ""); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty list: %d %s", w.Code, w.Body.String())
	}

	w := doBookmarkRequest(mux, "POST", "/api/messages/"+msg.ID+"/bookmark", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("toggle on: %d %s", w.Code, w.Body.String())
	}
	var on struct {
		Action   string       `json:"action"`
		Bookmark BookmarkView `json:"bookmark"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &on); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if on.Action != "created" || on.Bookmark.MessageID != msg.ID || on.Bookmark.SessionID != sess.ID {
		t.Fatalf("toggle on = %+v", on)
	}

	w = doBookmarkRequest(mux, "GET", "/api/sessions/"+sess.ID+"/bookmarks", "")
	var list []BookmarkView
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list after toggle on: %v %s", err, w.Body.String())
	}

	w = doBookmarkRequest(mux, "POST", "/api/messages/"+msg.ID+"/bookmark", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"action":"removed"`) {
		t.Fatalf("toggle off: %d %s", w.Code, w.Body.String())
	}
}

// Toggling a message that does not exist is 404 "message not found".
func TestBookmarksAPI_ToggleUnknownMessage404(t *testing.T) {
	_, mux := newTestAPI(t)
	w := doBookmarkRequest(mux, "POST", "/api/messages/no-such-message/bookmark", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d; body %s", w.Code, w.Body.String())
	}
	if msg := errorBody(t, w); msg != "message not found" {
		t.Fatalf("error %q", msg)
	}
}

// Deleting a missing bookmark is 404 with the store's own error text as the
// message — the raw sql.ErrNoRows text, not a friendlier string.
func TestBookmarksAPI_DeleteMissing404RawStoreError(t *testing.T) {
	_, mux := newTestAPI(t)
	w := doBookmarkRequest(mux, "DELETE", "/api/bookmarks/no-such-bookmark", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d; body %s", w.Code, w.Body.String())
	}
	if msg := errorBody(t, w); msg != "sql: no rows in result set" {
		t.Fatalf("error %q, want the raw store error text", msg)
	}
}

func TestBookmarksAPI_CreateThenDelete(t *testing.T) {
	a, mux := newTestAPI(t)
	sess, msg := seedBookmarkMessage(t, a)

	w := doBookmarkRequest(mux, "POST", "/api/bookmarks", `{"message_id":"`+msg.ID+`","session_id":"`+sess.ID+`","note":"n"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created BookmarkView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" || created.Note != "n" {
		t.Fatalf("created = %+v (%v)", created, err)
	}
	w = doBookmarkRequest(mux, "DELETE", "/api/bookmarks/"+created.ID, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":"`+created.ID+`"`) {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

// Autotitle reports a missing bookmark before any model is involved.
func TestBookmarksAPI_AutotitleMissing404(t *testing.T) {
	_, mux := newTestAPI(t)
	w := doBookmarkRequest(mux, "POST", "/api/bookmarks/no-such-bookmark/autotitle", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d; body %s", w.Code, w.Body.String())
	}
	if msg := errorBody(t, w); msg != "bookmark not found" {
		t.Fatalf("error %q", msg)
	}
}
