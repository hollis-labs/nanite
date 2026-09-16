package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func putJSON(t *testing.T, mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// The list endpoint has always redacted header values. Create and update
// echoed the saved record back verbatim, so the very request that sets a
// bearer token got it returned in plaintext — into a browser's network log,
// a proxy, or a terminal scrollback. Same record, same secret, same boundary.
const plaintextToken = "pfl_pat_notarealtokenbutlongenough"

func TestCreateAndUpdateResponsesRedactHeaders(t *testing.T) {
	_, mux := newTestAPI(t)

	create := postJSON(t, mux, "/api/mcp-servers", map[string]any{
		"name":           "redaction-probe",
		"transport_type": "streamable",
		"url":            "http://127.0.0.1:1/mcp",
		"headers":        `{"Authorization":"Bearer ` + plaintextToken + `"}`,
	})
	if create.Code != 201 {
		t.Fatalf("POST = %d, want 201: %s", create.Code, create.Body.String())
	}
	assertNoPlaintext(t, "create", create.Body.String())

	update := putJSON(t, mux, "/api/mcp-servers/redaction-probe", map[string]any{
		"transport_type": "streamable",
		"url":            "http://127.0.0.1:2/mcp",
		"headers":        `{"Authorization":"Bearer ` + plaintextToken + `"}`,
	})
	if update.Code != 200 {
		t.Fatalf("PUT = %d, want 200: %s", update.Code, update.Body.String())
	}
	assertNoPlaintext(t, "update", update.Body.String())
}

func assertNoPlaintext(t *testing.T, which, body string) {
	t.Helper()
	if strings.Contains(body, plaintextToken) {
		t.Fatalf("%s response leaked the token in plaintext: %s", which, body)
	}
	// The key must survive, so the UI can still show that the header is set.
	var got struct {
		Headers string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%s response not decodable: %v", which, err)
	}
	if !strings.Contains(got.Headers, "Authorization") {
		t.Fatalf("%s response dropped the header key entirely: %q", which, got.Headers)
	}
	if !strings.Contains(got.Headers, RedactedHeaderValue) {
		t.Fatalf("%s response headers not redacted: %q", which, got.Headers)
	}
}
