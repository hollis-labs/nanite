package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var consumerViewKeys = []string{"id", "slug", "name", "created_at"}

var documentViewKeys = []string{"id", "session_id", "name", "mime_type", "content", "size_bytes", "included", "full_content", "summary", "created_at", "updated_at"}

func TestConsumerViewJSON(t *testing.T) {
	var c store.Consumer
	populate(t, &c)
	assertKeys(t, "ConsumerView", mustJSON(t, consumerToView(&c)), consumerViewKeys)
	assertSameJSON(t, "populated", consumerToView(&c), c)
	assertSameJSON(t, "empty list", consumersToView([]store.Consumer{}), []store.Consumer{})
	assertSameJSON(t, "nil list", consumersToView(nil), []store.Consumer(nil))
}

func TestDocumentViewJSON(t *testing.T) {
	var d store.Document
	populate(t, &d)
	assertKeys(t, "DocumentView", mustJSON(t, documentToView(&d)), documentViewKeys)
	assertSameJSON(t, "populated", documentToView(&d), d)
	assertSameJSON(t, "zero", documentToView(&store.Document{}), store.Document{})
	assertSameJSON(t, "empty list", documentsToView([]store.Document{}), []store.Document{})
	assertSameJSON(t, "nil list", documentsToView(nil), []store.Document(nil))
}

func TestConsumers_StatusAndPatch(t *testing.T) {
	_, mux := newTestAPI(t)
	if w := mcpDo(mux, "POST", "/api/consumers", `{"name":"n"}`); w.Code != http.StatusBadRequest || errorBody(t, w) != "slug and name are required" {
		t.Fatalf("create missing slug: %d %s", w.Code, w.Body.String())
	}
	w := mcpDo(mux, "POST", "/api/consumers", `{"slug":"c2","name":"C2"}`)
	var c ConsumerView
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || w.Code != http.StatusCreated || c.ID == "" {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if rec := mcpDo(mux, "PUT", "/api/consumers/nope", `not json`); rec.Code != http.StatusNotFound || errorBody(t, rec) != "consumer not found" {
		t.Fatalf("update missing beats bad body: %d %s", rec.Code, rec.Body.String())
	}
	w = mcpDo(mux, "PUT", "/api/consumers/"+c.ID, `{"name":"Renamed"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"slug":"c2"`) || !strings.Contains(w.Body.String(), `"name":"Renamed"`) {
		t.Fatalf("update keeps slug: %d %s", w.Code, w.Body.String())
	}
	if rec := mcpDo(mux, "GET", "/api/consumers/"+c.ID, ""); !strings.Contains(rec.Body.String(), `"name":"Renamed"`) {
		t.Fatalf("get after update: %s", rec.Body.String())
	}
	if rec := mcpDo(mux, "DELETE", "/api/consumers/"+c.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := mcpDo(mux, "GET", "/api/consumers/"+c.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDocuments_HTTP(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := newB2aSession(t, a)
	base := "/api/sessions/" + sess.ID + "/documents"

	if w := mcpDo(mux, "GET", base, ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty list: %s", w.Body.String())
	}
	if w := mcpDo(mux, "POST", base, `{"name":"  "}`); w.Code != http.StatusBadRequest || errorBody(t, w) != "name is required" {
		t.Fatalf("no name: %d %s", w.Code, w.Body.String())
	}
	w := mcpDo(mux, "POST", base, `{"name":"spec.md","content":"hello","mime_type":"text/markdown","summary":"s","included":true,"full_content":true}`)
	var doc DocumentView
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || w.Code != http.StatusCreated || doc.ID == "" {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if rec := mcpDo(mux, "GET", "/api/documents/nope", ""); rec.Code != http.StatusNotFound || errorBody(t, rec) != "document not found" {
		t.Fatalf("get missing: %d %s", rec.Code, rec.Body.String())
	}
	// A bad body is a 400 before the document is looked up.
	if rec := mcpDo(mux, "PUT", "/api/documents/nope", `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body before lookup: %d %s", rec.Code, rec.Body.String())
	}
	// Only full_content is sent: included and summary are kept.
	w = mcpDo(mux, "PUT", "/api/documents/"+doc.ID, `{"full_content":false}`)
	var updated DocumentView
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil || w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if updated.FullContent || !updated.Included || updated.Summary != "s" || updated.Content != "hello" {
		t.Fatalf("updated = %+v", updated)
	}
	w = mcpDo(mux, "GET", "/api/documents/"+doc.ID, "")
	var stored DocumentView
	if err := json.Unmarshal(w.Body.Bytes(), &stored); err != nil || stored.FullContent || !stored.Included || stored.Summary != "s" {
		t.Fatalf("stored = %s", w.Body.String())
	}
	if rec := mcpDo(mux, "DELETE", "/api/documents/"+doc.ID, ""); strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("delete: %s", rec.Body.String())
	}

	cp := "/api/sessions/" + sess.ID + "/context-prompt"
	if rec := mcpDo(mux, "PUT", cp, `{"prompt":"be brief"}`); strings.TrimSpace(rec.Body.String()) != `{"prompt":"be brief"}` {
		t.Fatalf("set prompt: %s", rec.Body.String())
	}
	if rec := mcpDo(mux, "GET", cp, ""); strings.TrimSpace(rec.Body.String()) != `{"prompt":"be brief"}` {
		t.Fatalf("get prompt: %s", rec.Body.String())
	}
}
