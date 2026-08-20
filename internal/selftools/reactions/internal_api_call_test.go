package reactions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecuteInternalAPICall_Success_SubstitutesBodyTemplate(t *testing.T) {
	var gotMethod, gotContentType string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	e := NewEngine(nil, srv.Client(), nil)
	configJSON := `{"endpoint":"` + srv.URL + `","method":"post","body_template":{"id":"{{id}}","msg":"{{msg}}"}}`
	err := e.executeInternalAPICall(context.Background(), configJSON, map[string]any{"id": "42", "msg": "hi"})
	if err != nil {
		t.Fatalf("executeInternalAPICall: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST (lowercase config method must be normalized)", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["id"] != "42" || gotBody["msg"] != "hi" {
		t.Errorf("body = %+v, want {id: 42, msg: hi}", gotBody)
	}
}

func TestExecuteInternalAPICall_DefaultMethodIsPOST(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := NewEngine(nil, srv.Client(), nil)
	err := e.executeInternalAPICall(context.Background(), `{"endpoint":"`+srv.URL+`"}`, map[string]any{})
	if err != nil {
		t.Fatalf("executeInternalAPICall: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST default", gotMethod)
	}
}

func TestExecuteInternalAPICall_NonTwoXX_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server exploded", http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := NewEngine(nil, srv.Client(), nil)
	err := e.executeInternalAPICall(context.Background(), `{"endpoint":"`+srv.URL+`"}`, map[string]any{})
	if err == nil {
		t.Fatal("executeInternalAPICall against a 500 response returned nil error, want an error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention the non-2xx status code", err.Error())
	}
}

func TestExecuteInternalAPICall_MissingEndpoint(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	if err := e.executeInternalAPICall(context.Background(), `{}`, map[string]any{}); err == nil {
		t.Fatal("executeInternalAPICall with missing endpoint returned nil error, want an error")
	}
}

func TestExecuteInternalAPICall_InvalidConfigJSON(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	if err := e.executeInternalAPICall(context.Background(), `not json`, map[string]any{}); err == nil {
		t.Fatal("executeInternalAPICall with invalid JSON config returned nil error, want an error")
	}
}
