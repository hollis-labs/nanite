package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

var mcpServerViewKeys = []string{
	"id", "name", "transport_type", "command", "url", "args", "env", "enabled",
	"trust_tier", "env_allowlist", "headers", "created_at", "updated_at",
}

// legacyRedactedMCPJSON is what the list, create and update endpoints wrote
// before MCPServerView: a copy of the store row with its headers redacted by
// the old api-local redactHeaders, marshaled as is.
func legacyRedactedMCPJSON(t *testing.T, rows []store.MCPServerConfig) []byte {
	t.Helper()
	out := make([]store.MCPServerConfig, len(rows))
	copy(out, rows)
	for i := range out {
		parsed, err := mcp.ParseHeaderJSON(out[i].Headers)
		if err != nil || len(parsed) == 0 {
			out[i].Headers = "{}"
			continue
		}
		masked := make(map[string]string, len(parsed))
		for k := range parsed {
			masked[k] = "••••••••"
		}
		encoded, err := json.Marshal(masked)
		if err != nil {
			out[i].Headers = "{}"
			continue
		}
		out[i].Headers = string(encoded)
	}
	return mustJSON(t, out)
}

func populatedMCPServerConfig(t *testing.T) store.MCPServerConfig {
	t.Helper()
	var cfg store.MCPServerConfig
	rv := reflect.ValueOf(&cfg).Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(fmt.Sprintf("value-%d", i))
		case reflect.Bool:
			f.SetBool(true)
		default:
			t.Fatalf("store.MCPServerConfig.%s has kind %s; teach populatedMCPServerConfig about it", rv.Type().Field(i).Name, f.Kind())
		}
	}
	cfg.Headers = `{"Authorization":"Bearer secret-token","X-Tenant":"<acme & co>"}`
	return cfg
}

func TestMCPServerViewJSON(t *testing.T) {
	full := populatedMCPServerConfig(t)
	raw := mustJSON(t, mcpServerToView(&full))
	assertKeys(t, "MCPServerView", raw, mcpServerViewKeys)
	if strings.Contains(string(raw), "secret-token") {
		t.Fatalf("view carries the header value: %s", raw)
	}
	if full.Headers != `{"Authorization":"Bearer secret-token","X-Tenant":"<acme & co>"}` {
		t.Fatalf("building the view changed the row's headers: %s", full.Headers)
	}

	for name, rows := range map[string][]store.MCPServerConfig{
		"populated":           {full},
		"zero":                {{}},
		"unparseable headers": {{Name: "x", Headers: `{"Authorization":"Bearer oops`}},
		"empty headers":       {{Name: "x", Headers: "{}"}},
		"empty list":          {},
		"two rows":            {full, {Name: "y", Headers: `{"A":"b"}`}},
	} {
		if got, want := mustJSON(t, mcpServersToView(rows)), legacyRedactedMCPJSON(t, rows); !bytes.Equal(got, want) {
			t.Errorf("%s: view JSON differs from the legacy JSON\n got: %s\nwant: %s", name, got, want)
		}
	}
	if got := string(mustJSON(t, mcpServersToView(nil))); got != "[]" {
		t.Errorf("nil list = %s, want []", got)
	}
}

const mcpHeaderSecret = "hdr_notarealtokenbutlongenough" // #nosec G101 -- fake token, not a credential
const mcpEnvSecret = "ghp_notarealtokenbutlongenough"    // #nosec G101 -- fake token, not a credential

func mcpDo(mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func mcpSecretServerBody(name string) string {
	return `{"name":"` + name + `","transport_type":"stdio","command":"true",` +
		`"env":"[\"GITHUB_TOKEN=` + mcpEnvSecret + `\"]",` +
		`"headers":"{\"Authorization\":\"Bearer ` + mcpHeaderSecret + `\"}"}`
}

// TestMCPServers_NoReadEndpointReturnsAHeaderValue covers every endpoint that
// returns MCP server data: list, create, update, import and export.
func TestMCPServers_NoReadEndpointReturnsAHeaderValue(t *testing.T) {
	_, mux := newTestAPI(t)

	checks := []struct {
		name, method, path, body string
		code                     int
	}{
		{"create", "POST", "/api/mcp-servers", mcpSecretServerBody("secrets"), http.StatusCreated},
		{"list", "GET", "/api/mcp-servers", "", http.StatusOK},
		{"update", "PUT", "/api/mcp-servers/secrets", mcpSecretServerBody("secrets"), http.StatusOK},
		{"export", "GET", "/api/mcp-servers/export", "", http.StatusOK},
		{"import", "POST", "/api/mcp-servers/import",
			`{"mcpServers":{"imported":{"command":"true","env":{"TOKEN":"` + mcpEnvSecret + `"}}}}`, http.StatusOK},
	}
	for _, c := range checks {
		w := mcpDo(mux, c.method, c.path, c.body)
		if w.Code != c.code {
			t.Fatalf("%s: status %d, want %d: %s", c.name, w.Code, c.code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), mcpHeaderSecret) {
			t.Errorf("%s response carries the header value: %s", c.name, w.Body.String())
		}
	}

	// The list keeps the header key and shows the placeholder.
	w := mcpDo(mux, "GET", "/api/mcp-servers", "")
	var rows []MCPServerView
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, r := range rows {
		if r.Name == "secrets" && r.Headers != `{"Authorization":"`+service.RedactedHeaderValue+`"}` {
			t.Errorf("list headers = %q, want the key with the placeholder", r.Headers)
		}
	}

	// Import answers with names only.
	w = mcpDo(mux, "POST", "/api/mcp-servers/import", `{"mcpServers":{"imported":{"command":"true"}}}`)
	if got := strings.TrimSpace(w.Body.String()); got != `{"created":null,"skipped":["imported"]}` {
		t.Errorf("import body = %s", got)
	}
}

// TestMCPServers_EnvReturnedUnredacted_CurrentBehaviour_PendingCW20260930_0117
// pins that env values, unlike header values, are returned in plaintext by
// list, create, update and export. CW-20260930-0117 decides whether they
// should be; whoever changes that flips this test.
func TestMCPServers_EnvReturnedUnredacted_CurrentBehaviour_PendingCW20260930_0117(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, c := range []struct{ name, method, path, body string }{
		{"create", "POST", "/api/mcp-servers", mcpSecretServerBody("envprobe")},
		{"list", "GET", "/api/mcp-servers", ""},
		{"update", "PUT", "/api/mcp-servers/envprobe", mcpSecretServerBody("envprobe")},
		{"export", "GET", "/api/mcp-servers/export", ""},
	} {
		w := mcpDo(mux, c.method, c.path, c.body)
		if !strings.Contains(w.Body.String(), mcpEnvSecret) {
			t.Errorf("%s: env value no longer returned; if CW-20260930-0117 landed, flip this test: %s", c.name, w.Body.String())
		}
	}
}

// TestMCPServers_UIShapedPutPreservesHeadersTrustTierAllowlist_CW20260930_0116
// pins the fix for CW-20260930-0116: the settings UI's PUT payload
// (ToolDashboard formToPayload) omits headers, trust_tier and env_allowlist,
// and saving from it used to reset all three, dropping a stored credential.
// Omitted fields now keep their stored values.
func TestMCPServers_UIShapedPutPreservesHeadersTrustTierAllowlist_CW20260930_0116(t *testing.T) {
	a, mux := newTestAPI(t)
	create := `{"name":"remote","transport_type":"streamable","url":"http://127.0.0.1:1/mcp",` +
		`"headers":"{\"Authorization\":\"Bearer ` + mcpHeaderSecret + `\"}","trust_tier":"first_party","env_allowlist":"[\"HOME\"]"}`
	if w := mcpDo(mux, "POST", "/api/mcp-servers", create); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	ui := `{"name":"remote","transport_type":"streamable","command":"","url":"http://127.0.0.1:1/mcp","args":"[]","env":"[]","enabled":true}`
	if w := mcpDo(mux, "PUT", "/api/mcp-servers/remote", ui); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	row, err := a.Services.Store.GetMCPServer(context.Background(), "remote")
	if err != nil || row == nil {
		t.Fatalf("GetMCPServer: %v %v", row, err)
	}
	if row.Headers != `{"Authorization":"Bearer `+mcpHeaderSecret+`"}` || row.TrustTier != "first_party" || row.EnvAllowlist != `["HOME"]` {
		t.Fatalf("stored after UI-shaped PUT: headers=%q trust_tier=%q env_allowlist=%q, want all three kept",
			row.Headers, row.TrustTier, row.EnvAllowlist)
	}
}

// mcpFullServer creates a stdio server with every settable field set, and
// returns the stored row.
func mcpFullServer(t *testing.T, a *API, mux http.Handler) *store.MCPServerConfig {
	t.Helper()
	create := `{"name":"full","transport_type":"stdio","command":"run","url":"http://127.0.0.1:1/x",` +
		`"args":"[\"-v\"]","env":"[\"K=V\"]","trust_tier":"first_party","env_allowlist":"[\"HOME\"]",` +
		`"headers":"{\"Authorization\":\"Bearer ` + mcpHeaderSecret + `\"}"}`
	if w := mcpDo(mux, "POST", "/api/mcp-servers", create); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	row, err := a.Services.Store.GetMCPServer(context.Background(), "full")
	if err != nil || row == nil {
		t.Fatalf("GetMCPServer: %v %v", row, err)
	}
	return row
}

// Each settable field, when the PUT omits it (or sends null), keeps its
// stored value. The table sends every other field so each row isolates one.
func TestMCPServers_PutOmittedFieldKeepsStoredValue(t *testing.T) {
	sent := map[string]string{
		"transport_type": `"stdio"`,
		"command":        `"run2"`,
		"url":            `"http://127.0.0.1:2/x"`,
		"args":           `"[\"-q\"]"`,
		"env":            `"[\"K=W\"]"`,
		"enabled":        `false`,
		"trust_tier":     `"third_party_http"`,
		"env_allowlist":  `"[\"PATH\"]"`,
		"headers":        `"{\"Authorization\":\"Bearer other\"}"`,
	}
	field := func(r *store.MCPServerConfig, name string) any {
		switch name {
		case "transport_type":
			return r.TransportType
		case "command":
			return r.Command
		case "url":
			return r.URL
		case "args":
			return r.Args
		case "env":
			return r.Env
		case "enabled":
			return r.Enabled
		case "trust_tier":
			return r.TrustTier
		case "env_allowlist":
			return r.EnvAllowlist
		case "headers":
			return r.Headers
		}
		t.Fatalf("unknown field %s", name)
		return nil
	}
	for omitted := range sent {
		for _, mode := range []string{"omitted", "null"} {
			t.Run(omitted+"/"+mode, func(t *testing.T) {
				a, mux := newTestAPI(t)
				before := mcpFullServer(t, a, mux)
				var parts []string
				for k, v := range sent {
					if k == omitted {
						if mode == "null" {
							parts = append(parts, `"`+k+`":null`)
						}
						continue
					}
					parts = append(parts, `"`+k+`":`+v)
				}
				if w := mcpDo(mux, "PUT", "/api/mcp-servers/full", "{"+strings.Join(parts, ",")+"}"); w.Code != http.StatusOK {
					t.Fatalf("update: %d %s", w.Code, w.Body.String())
				}
				after, err := a.Services.Store.GetMCPServer(context.Background(), "full")
				if err != nil || after == nil {
					t.Fatalf("GetMCPServer: %v %v", after, err)
				}
				if got, want := field(after, omitted), field(before, omitted); got != want {
					t.Fatalf("%s %s: stored %v, want the kept %v", omitted, mode, got, want)
				}
			})
		}
	}
}

// Sending a field explicitly empty still clears it; the store applies its
// defaults where it has one.
func TestMCPServers_PutExplicitEmptyClears(t *testing.T) {
	a, mux := newTestAPI(t)
	mcpFullServer(t, a, mux)
	body := `{"command":"","url":"","args":"","env":"","enabled":false,"trust_tier":"","env_allowlist":"","headers":"{}"}`
	if w := mcpDo(mux, "PUT", "/api/mcp-servers/full", body); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	row, err := a.Services.Store.GetMCPServer(context.Background(), "full")
	if err != nil || row == nil {
		t.Fatalf("GetMCPServer: %v %v", row, err)
	}
	got := []any{row.Command, row.URL, row.Args, row.Env, row.Enabled, row.TrustTier, row.EnvAllowlist, row.Headers, row.TransportType}
	want := []any{"", "", "", "", false, store.TrustTierThirdPartyHTTP, "[]", "{}", store.TransportStdio}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after explicit clears got %v, want %v", got, want)
	}

	// "headers":"" clears too.
	mcpDo(mux, "PUT", "/api/mcp-servers/full", `{"headers":"{\"A\":\"b\"}"}`)
	mcpDo(mux, "PUT", "/api/mcp-servers/full", `{"headers":""}`)
	if row, _ = a.Services.Store.GetMCPServer(context.Background(), "full"); row.Headers != "{}" {
		t.Fatalf(`headers after "headers":"" = %q, want {}`, row.Headers)
	}
}

// The PUT response is the stored row: carried values, the stored created_at,
// and headers still redacted.
func TestMCPServers_PutResponseIsTheStoredRow(t *testing.T) {
	a, mux := newTestAPI(t)
	before := mcpFullServer(t, a, mux)
	w := mcpDo(mux, "PUT", "/api/mcp-servers/full", `{"command":"run2","created_at":"client-sent"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	var got MCPServerView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	after, err := a.Services.Store.GetMCPServer(context.Background(), "full")
	if err != nil || after == nil {
		t.Fatalf("GetMCPServer: %v %v", after, err)
	}
	if want := mcpServerToView(after); got != want {
		t.Fatalf("response is not the stored row\n got: %+v\nwant: %+v", got, want)
	}
	if got.CreatedAt != before.CreatedAt || got.Command != "run2" || got.URL != before.URL || !got.Enabled {
		t.Fatalf("response = %+v, want stored created_at %q, the new command and carried url/enabled", got, before.CreatedAt)
	}
	if strings.Contains(w.Body.String(), mcpHeaderSecret) || got.Headers != `{"Authorization":"`+service.RedactedHeaderValue+`"}` {
		t.Fatalf("response headers not redacted: %s", w.Body.String())
	}
}

func TestMCPServers_PutCarriesForwardRedactedHeaders(t *testing.T) {
	a, mux := newTestAPI(t)
	create := `{"name":"cf","transport_type":"streamable","url":"http://127.0.0.1:1/mcp",` +
		`"headers":"{\"Authorization\":\"Bearer ` + mcpHeaderSecret + `\",\"X-Tenant\":\"old\"}"}`
	if w := mcpDo(mux, "POST", "/api/mcp-servers", create); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	ph := service.RedactedHeaderValue
	put := `{"transport_type":"streamable","url":"http://127.0.0.1:1/mcp",` +
		`"headers":"{\"Authorization\":\"` + ph + `\",\"X-Tenant\":\"new\",\"X-Invented\":\"` + ph + `\"}"}`
	if w := mcpDo(mux, "PUT", "/api/mcp-servers/cf", put); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	row, err := a.Services.Store.GetMCPServer(context.Background(), "cf")
	if err != nil || row == nil {
		t.Fatalf("GetMCPServer: %v %v", row, err)
	}
	got, err := mcp.ParseHeaderJSON(row.Headers)
	if err != nil {
		t.Fatalf("stored headers: %v", err)
	}
	want := map[string]string{"Authorization": "Bearer " + mcpHeaderSecret, "X-Tenant": "new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored headers = %v, want %v", got, want)
	}
}

func TestMCPServers_Precedence(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, c := range []struct {
		name, method, path, body string
		code                     int
		msg                      string
	}{
		{"create bad body", "POST", "/api/mcp-servers", `not json`, 400, "invalid request body"},
		{"create no name", "POST", "/api/mcp-servers", `{"transport_type":"grpc"}`, 400, "name is required"},
		{"create bad transport", "POST", "/api/mcp-servers", `{"name":"p","transport_type":"grpc"}`, 400, service.TransportTypeError},
		{"create", "POST", "/api/mcp-servers", `{"name":"p","transport_type":"sse","url":"http://127.0.0.1:1/sse"}`, 201, ""},
		{"create duplicate", "POST", "/api/mcp-servers", `{"name":"p"}`, 409, "server with this name already exists"},
		{"update missing beats bad body", "PUT", "/api/mcp-servers/nope", `not json`, 404, "server not found"},
		{"update bad body", "PUT", "/api/mcp-servers/p", `not json`, 400, "invalid request body"},
		{"update bad transport", "PUT", "/api/mcp-servers/p", `{"transport_type":"grpc"}`, 400, service.TransportTypeError},
		{"delete missing", "DELETE", "/api/mcp-servers/nope", "", 404, `mcp server "nope" not found`},
	} {
		w := mcpDo(mux, c.method, c.path, c.body)
		if w.Code != c.code {
			t.Fatalf("%s: status %d, want %d: %s", c.name, w.Code, c.code, w.Body.String())
		}
		if c.msg != "" {
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] != c.msg {
				t.Fatalf("%s: body %s, want error %q", c.name, w.Body.String(), c.msg)
			}
		}
	}

	// An empty transport keeps the stored one on update and means stdio on create.
	w := mcpDo(mux, "PUT", "/api/mcp-servers/p", `{"url":"http://127.0.0.1:2/sse"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"transport_type":"sse"`) {
		t.Fatalf("update with empty transport: %d %s", w.Code, w.Body.String())
	}
	w = mcpDo(mux, "POST", "/api/mcp-servers", `{"name":"q","command":"true"}`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"transport_type":"stdio"`) {
		t.Fatalf("create with empty transport: %d %s", w.Code, w.Body.String())
	}
}

// failingMCPStore fails every read.
type failingMCPStore struct{}

func (failingMCPStore) ListMCPServers(context.Context) ([]store.MCPServerConfig, error) {
	return nil, errors.New("db down")
}
func (failingMCPStore) GetMCPServer(context.Context, string) (*store.MCPServerConfig, error) {
	return nil, errors.New("db down")
}
func (failingMCPStore) CreateMCPServer(context.Context, *store.MCPServerConfig) error { return nil }
func (failingMCPStore) UpdateMCPServer(context.Context, *store.MCPServerConfig) error { return nil }
func (failingMCPStore) DeleteMCPServer(context.Context, string) error                 { return nil }

func TestMCPServers_UpdateLoadFailureBeforeDecode(t *testing.T) {
	a := &API{Services: &service.Container{MCPServers: service.NewMCPServerService(failingMCPStore{}, nil, nil)}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/mcp-servers/{name}", a.handleUpdateMCPServer)
	w := mcpDo(mux, "PUT", "/api/mcp-servers/x", `not json`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "db down") {
		t.Fatalf("PUT with failing load: %d %s, want 500 db down", w.Code, w.Body.String())
	}
}
