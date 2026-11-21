package providertest

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Every committed fixture passes the same check a recording passes before
// it is saved, so a hand edit cannot slip a host or credential in.
func TestCommittedFixturesAreClean(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "*", "testdata", "replay", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("fixtures %v, %v", files, err)
	}
	for _, path := range files {
		f := Load(t, path)
		if f.Source != "recorded" {
			t.Fatalf("%s: committed fixtures are recordings; derive failures in test code (source %q)", path, f.Source)
		}
		CheckClean(t, path, f)
	}
}

func TestCheckCleanCatches(t *testing.T) {
	for _, bad := range []Fixture{
		{Response: Response{Body: "see https://example.test/x"}},
		{Request: Request{Path: "/api/gw.internal/v1/messages"}},
		{Response: Response{Body: "host 192.0.2.7 answered"}},
		{Response: Response{Body: "localhost:8080"}},
		{Response: Response{Body: "key sk-svcacct-abc123def"}},
		{Response: Response{Headers: map[string]string{"X": "bearer abc"}}},
		{Source: "derived from eyJhbGciOiJIUzI1NiJ9"},
	} {
		ft := &fakeT{}
		func() {
			defer func() { _ = recover() }()
			CheckClean(ft, "fixture", bad)
		}()
		if !ft.failed {
			t.Fatalf("not caught: %+v", bad)
		}
	}
	ok := &fakeT{}
	CheckClean(ok, "fixture", Fixture{Source: "recorded", Request: Request{Path: "/v1/messages"},
		Response: Response{Body: strings.Repeat("desk-pilot risk-free ", 3) + `"model":"claude-haiku-5-5"`}})
	if ok.failed {
		t.Fatal("a clean fixture was refused")
	}
}

// fakeT records a failure; Fatalf panics to stop the check, as t.Fatalf
// stops the test goroutine.
type fakeT struct {
	testing.TB
	failed bool
}

func (f *fakeT) Helper() {}

func (f *fakeT) Fatalf(string, ...any) {
	f.failed = true
	panic("fatal")
}

// The replay server answers a request only for the fixture's method and
// API path, so a test cannot pass against the wrong endpoint.
func TestServerChecksTheRequest(t *testing.T) {
	f := Fixture{Request: Request{Method: http.MethodPost, Path: "/v1/messages"}, Response: Response{Status: http.StatusOK, Body: "ok"}}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/gateway/v1/messages", http.StatusOK},
		{http.MethodPost, "/v1/complete", http.StatusInternalServerError},
		{http.MethodGet, "/v1/messages", http.StatusInternalServerError},
	} {
		srv := Serve(t, f)
		req, err := http.NewRequestWithContext(t.Context(), tc.method, srv.URL+tc.path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("%s %s: status %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}
