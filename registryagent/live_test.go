package registryagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func wireRecord(r Record, publication string) liveRecord {
	return liveRecord{ID: r.Reference.Kind + ":" + r.Reference.ID, Kind: r.Reference.Kind, Schema: r.Reference.Kind + "/1", Revision: r.Revision, Publication: publication, Payload: r.Payload}
}

func TestLiveMaterialize(t *testing.T) {
	for _, publication := range []string{"draft", "published"} {
		t.Run(publication, func(t *testing.T) {
			records, ref := fixture(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-reader" {
					t.Error("wrong method or authorization")
					w.WriteHeader(403)
					return
				}
				var args map[string]string
				if err := json.Unmarshal([]byte(r.URL.Query().Get("args")), &args); err != nil {
					t.Error(err)
				}
				state := publication
				switch r.URL.Path {
				case "/v1/record":
					if args["publication"] != publication {
						t.Error("publication not explicit")
					}
				case "/v1/revision":
					if args["revision"] != "r1" || args["publication"] != "" {
						t.Error("bad revision args")
					}
					state = "draft"
				default:
					t.Error("unexpected route")
				}
				for _, record := range records {
					if r.URL.Query().Get("id") == record.Reference.Kind+":"+record.Reference.ID {
						_ = json.NewEncoder(w).Encode(wireRecord(record, state))
						return
					}
				}
				w.WriteHeader(404)
			}))
			defer server.Close()
			live, err := NewLive(LiveConfig{URL: server.URL, Authority: "registry", Publication: publication, Token: func(context.Context) (string, error) { return "test-reader", nil }})
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "agent")
			result, err := Materialize(t.Context(), live, ref, dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range result.Manifest.Sources {
				if source.Publication != publication {
					t.Fatal("publication not recorded")
				}
			}
			again, err := Materialize(t.Context(), live, ref, dir)
			if err != nil || again.Changed {
				t.Fatal("unchanged materialization", err)
			}
		})
	}
}

func TestLiveRejectsResponses(t *testing.T) {
	records, ref := fixture(t)
	// Each mode must fail at its own check: a response that is wrong in two
	// ways would keep failing after the check under test were removed.
	want := map[string]string{
		"id": "identity or revision mismatch", "kind": "kind mismatch", "schema": "kind mismatch",
		"revision": "identity or revision mismatch", "publication": "publication mismatch",
		"payload-id": "payload identity mismatch", "payload-schema": "unsupported schema",
		"duplicate": "duplicate JSON key", "trailing": "trailing Registry JSON",
		"large": "Registry response exceeds size limit", "status": "HTTP 403",
		"status-401": "HTTP 401", "status-500": "HTTP 500", "invalid": "invalid JSON",
		"pin-unpublished": "not currently published", "pin-payload": "not currently published",
	}
	for _, mode := range []string{"id", "kind", "schema", "revision", "publication", "payload-id", "payload-schema", "duplicate", "trailing", "large", "status", "status-401", "status-500", "invalid", "pin-unpublished", "pin-payload"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				record := wireRecord(records[0], "draft")
				switch mode {
				case "id":
					record.ID = "agent:other"
				case "kind":
					record.Kind = "skill"
				case "schema":
					record.Schema = "agent/2"
				case "revision":
					record.Revision = "wrong"
				case "publication":
					record.Publication = "published"
				case "payload-id":
					record.Payload = []byte(strings.Replace(string(record.Payload), `"id":"fixture"`, `"id":"other"`, 1))
				case "payload-schema":
					record.Payload = []byte(strings.Replace(string(record.Payload), `"schema":"agent/1"`, `"schema":"agent/2"`, 1))
				case "duplicate":
					// A valid envelope under last-wins decoding: only the
					// duplicate-key check can refuse it.
					raw, _ := json.Marshal(record)
					_, _ = w.Write(append([]byte(`{"revision_id":"decoy",`), raw[1:]...))
					return
				case "trailing":
					_, _ = fmt.Fprint(w, `{} {}`)
					return
				case "large":
					raw, _ := json.Marshal(record)
					// A valid envelope if the size gate were removed, not just invalid whitespace.
					raw = append(raw[:len(raw)-1], []byte(",\"padding\":\""+strings.Repeat("x", 2*maxRecordBytes)+"\"}")...)
					_, _ = w.Write(raw)
					return
				case "status":
					w.WriteHeader(403)
					_, _ = fmt.Fprint(w, "remote-sensitive-diagnostic")
					return
				case "status-401", "status-500":
					// A valid record envelope: only the status check refuses it.
					w.WriteHeader(map[string]int{"status-401": 401, "status-500": 500}[mode])
					_ = json.NewEncoder(w).Encode(record)
					return
				case "invalid":
					_, _ = fmt.Fprint(w, "remote-sensitive-diagnostic")
					return
				case "pin-unpublished":
					if r.URL.Path == "/v1/record" {
						record.Revision = "r2"
						record.Publication = "published"
					}
				case "pin-payload":
					// Same revision id, but the published payload differs from
					// the pinned revision's (and is itself valid).
					if r.URL.Path == "/v1/record" {
						record.Publication = "published"
						record.Payload = []byte(strings.Replace(string(record.Payload), `"name":"fixture"`, `"name":"changed"`, 1))
					}
				}
				_ = json.NewEncoder(w).Encode(record)
			}))
			defer server.Close()
			publication := "draft"
			if mode == "pin-unpublished" || mode == "pin-payload" {
				publication = "published"
			}
			live, err := NewLive(LiveConfig{URL: server.URL, Authority: "registry", Publication: publication})
			if err != nil {
				t.Fatal(err)
			}
			_, err = live.Get(t.Context(), ref, "r1")
			if err == nil || strings.Contains(err.Error(), "remote-sensitive-diagnostic") {
				t.Fatal("unsafe or successful response", err)
			}
			if !strings.Contains(err.Error(), want[mode]) {
				t.Fatalf("rejected for the wrong reason: want %q, got %v", want[mode], err)
			}
		})
	}
}

func TestLiveTransportBoundaries(t *testing.T) {
	records, ref := fixture(t)
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer redirect.Close()
	live, err := NewLive(LiveConfig{URL: redirect.URL, Authority: "registry"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := live.Get(t.Context(), ref, ""); err == nil || called {
		t.Fatal("redirect followed")
	}
	foreign := ref
	foreign.Authority = "elsewhere"
	if _, err := live.Get(t.Context(), foreign, ""); err == nil {
		t.Fatal("foreign authority accepted")
	}
	for _, config := range []LiveConfig{{URL: "file:///tmp/x", Authority: "registry"}, {URL: redirect.URL + "?key=value", Authority: "registry"}, {URL: redirect.URL, Authority: "registry", Publication: "any"}} {
		if _, err := NewLive(config); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	live.config.Token = func(context.Context) (string, error) { return "", errors.New("remote-sensitive-diagnostic") }
	if _, err := live.Get(t.Context(), ref, ""); err == nil || strings.Contains(err.Error(), "remote-sensitive-diagnostic") {
		t.Fatal("credential error leaked", err)
	}
	waiting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer waiting.Close()
	live, err = NewLive(LiveConfig{URL: waiting.URL, Authority: "registry"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := live.Get(ctx, ref, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancellation lost", err)
	}
	// The materializer must independently distrust arbitrary Resolver implementations.
	for _, mode := range []string{"identity", "revision"} {
		t.Run(mode, func(t *testing.T) {
			resolver := resolverFunc(func(ctx context.Context, ref Reference, revision string) (Record, error) {
				r, err := records.Get(ctx, ref, revision)
				if mode == "identity" {
					r.Reference.ID = "other"
				} else if revision != "" {
					r.Revision = "other"
				}
				return r, err
			})
			if _, err := Materialize(t.Context(), resolver, ref, filepath.Join(t.TempDir(), "agent")); err == nil {
				t.Fatal("resolver mismatch accepted")
			}
		})
	}
}

func TestCasterCommand(t *testing.T) {
	data := []byte("binary\x00payload\n")
	sum := sha256.Sum256(data)
	ref := "cas://sha256/" + hex.EncodeToString(sum[:])
	exe := filepath.Join(t.TempDir(), "caster")
	// A test executable verifies fixed arguments and emits binary stdout.
	script := "#!/bin/sh\n[ \"$1\" = get ] && [ \"$2\" = '" + ref + "' ] || exit 2\nprintf 'binary\\000payload\\n'\n"
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil { // #nosec G306 -- executable test fixture in a private temporary directory.
		t.Fatal(err)
	}
	c := CasterCommand{Executable: exe}
	got, err := c.Fetch(t.Context(), ref)
	if err != nil || string(got) != string(data) {
		t.Fatal("binary fetch", err)
	}
	if _, err := c.Fetch(t.Context(), "cas://sha256/"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("bad artifact accepted")
	}
	if _, err := (CasterCommand{Executable: "caster"}).Fetch(t.Context(), ref); err == nil {
		t.Fatal("PATH lookup accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Fetch(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel lost", err)
	}
	cancelled := false
	b := boundedOutput{cancel: func() { cancelled = true }}
	b.buffer.Grow(maxArtifactBytes)
	_, _ = b.Write(make([]byte, maxArtifactBytes))
	if _, err := b.Write([]byte{0}); err == nil || !cancelled || !b.exceeded {
		t.Fatal("size not bounded")
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf wrong\n"), 0700); err != nil { // #nosec G306 -- executable test fixture.
		t.Fatal(err)
	}
	if _, err := c.Fetch(t.Context(), ref); err == nil {
		t.Fatal("hash mismatch accepted")
	}
}

func TestCanonicalReferences(t *testing.T) {
	records, ref := fixture(t)
	for i := range records {
		records[i].Reference.ID = records[i].Reference.Kind + ":" + records[i].Reference.ID
	}
	var body map[string]any
	if err := json.Unmarshal(records[0].Payload, &body); err != nil {
		t.Fatal(err)
	}
	body["launch_profile"] = versioned{records[1].Reference, "r1"}
	body["skills"] = []Reference{records[2].Reference}
	body["context"] = []Reference{records[3].Reference}
	body["variants"] = []Reference{records[4].Reference}
	records[0].Payload, _ = json.Marshal(body)
	ref.ID = "agent:" + ref.ID
	if _, err := Materialize(t.Context(), records, ref, filepath.Join(t.TempDir(), "fake")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, record := range records {
			if record.Reference.ID == r.URL.Query().Get("id") {
				wire := wireRecord(record, "draft")
				wire.ID = record.Reference.ID
				_ = json.NewEncoder(w).Encode(wire)
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	live, err := NewLive(LiveConfig{URL: server.URL, Authority: "registry", Publication: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(t.Context(), live, ref, filepath.Join(t.TempDir(), "live")); err != nil {
		t.Fatal(err)
	}
}

func TestLiveHTTPSAndNotFound(t *testing.T) {
	for _, endpoint := range []string{"http://example.invalid", "http://192.0.2.1", "http://localhost.example.invalid"} {
		if _, err := NewLive(LiveConfig{URL: endpoint, Authority: "registry"}); err == nil {
			t.Fatal("insecure remote endpoint accepted")
		}
	}
	for _, endpoint := range []string{"https://example.invalid", "http://localhost:1234", "http://127.0.0.2:1234", "http://[::1]:1234"} {
		if _, err := NewLive(LiveConfig{URL: endpoint, Authority: "registry"}); err != nil {
			t.Fatal("secure/loopback endpoint refused", err)
		}
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	live, err := NewLive(LiveConfig{URL: server.URL, Authority: "registry"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = live.Get(t.Context(), Reference{Authority: "registry", Kind: "agent", ID: "missing"}, "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("not-found identity lost", err)
	}
}

func TestLiveEscapedIdentityAndCanonicalPinnedPayload(t *testing.T) {
	records, ref := fixture(t)
	ref.ID = "fixture&x=y#tail"
	var payload map[string]any
	if err := json.Unmarshal(records[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload["id"] = ref.ID
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	record := wireRecord(Record{Reference: ref, Revision: "r1", Payload: raw}, "draft")
	reordered := []byte(`{"schema":"agent/1",` + strings.TrimPrefix(strings.Replace(string(raw), `"schema":"agent/1",`, "", 1), "{"))
	if !json.Valid(reordered) || string(raw) == string(reordered) {
		t.Fatal("bad reorder fixture")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "agent:"+ref.ID || len(r.URL.Query()) != 2 || r.URL.Fragment != "" {
			t.Error("ID was not preserved as one query value")
		}
		wire := record
		if r.URL.Path == "/v1/record" {
			wire.Publication = "published"
			wire.Payload = reordered
		}
		_ = json.NewEncoder(w).Encode(wire)
	}))
	defer server.Close()
	live, err := NewLive(LiveConfig{URL: server.URL, Authority: "registry"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := live.Get(t.Context(), ref, "r1")
	if err != nil || result.Revision != "r1" || result.Publication != "published" {
		t.Fatal("equivalent pinned JSON refused", err)
	}
	if equalJSON([]byte(`{"n":9007199254740992}`), []byte(`{"n":9007199254740993}`)) {
		t.Fatal("comparison rounded numbers")
	}
}
