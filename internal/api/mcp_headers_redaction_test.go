package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestRedactHeaders_keepsKeysAndDropsValues(t *testing.T) {
	in := []store.MCPServerConfig{{
		Name:    "workday",
		Headers: `{"Authorization":"Bearer supersecret","X-Tenant":"adtran"}`,
	}}
	out := redactHeaders(in)

	if strings.Contains(out[0].Headers, "supersecret") {
		t.Fatalf("token survived redaction: %s", out[0].Headers)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out[0].Headers), &got); err != nil {
		t.Fatalf("redacted headers are not valid json: %v", err)
	}
	for _, k := range []string{"Authorization", "X-Tenant"} {
		if got[k] != RedactedHeaderValue {
			t.Fatalf("key %q: got %q, want the redaction marker", k, got[k])
		}
	}
	// The input is also what the register path reads — redacting in place
	// would register the server with a token of bullets.
	if !strings.Contains(in[0].Headers, "supersecret") {
		t.Fatal("redactHeaders mutated its input")
	}
}

func TestRedactHeaders_hidesUnparseableValuesToo(t *testing.T) {
	in := []store.MCPServerConfig{{Name: "x", Headers: `{"Authorization":"Bearer oops`}}
	if out := redactHeaders(in); strings.Contains(out[0].Headers, "oops") {
		t.Fatalf("unparseable headers leaked: %s", out[0].Headers)
	}
}

func TestMergeRedactedHeaders_restoresUnchangedSecrets(t *testing.T) {
	stored := `{"Authorization":"Bearer supersecret","X-Tenant":"adtran"}`
	// What a UI sends back after loading the redacted list and editing nothing.
	incoming := `{"Authorization":"` + RedactedHeaderValue + `","X-Tenant":"` + RedactedHeaderValue + `"}`

	merged := mergeRedactedHeaders(incoming, stored)

	var got map[string]string
	if err := json.Unmarshal([]byte(merged), &got); err != nil {
		t.Fatalf("merged headers are not valid json: %v", err)
	}
	if got["Authorization"] != "Bearer supersecret" {
		t.Fatalf("token was not restored: %q", got["Authorization"])
	}
	if got["X-Tenant"] != "adtran" {
		t.Fatalf("second header was not restored: %q", got["X-Tenant"])
	}
}

func TestMergeRedactedHeaders_letsRealEditsThrough(t *testing.T) {
	stored := `{"Authorization":"Bearer old"}`
	incoming := `{"Authorization":"Bearer new"}`
	merged := mergeRedactedHeaders(incoming, stored)
	if !strings.Contains(merged, "Bearer new") {
		t.Fatalf("a deliberate change was discarded: %s", merged)
	}
}

func TestMergeRedactedHeaders_dropsARedactedKeyThatNeverExisted(t *testing.T) {
	// A client inventing a key with the marker as its value has given us no
	// value at all; storing bullets would be worse than storing nothing.
	stored := `{"Authorization":"Bearer old"}`
	incoming := `{"Authorization":"Bearer old","X-Invented":"` + RedactedHeaderValue + `"}`
	merged := mergeRedactedHeaders(incoming, stored)
	if strings.Contains(merged, "X-Invented") {
		t.Fatalf("invented redacted key was stored: %s", merged)
	}
}

func TestMergeRedactedHeaders_clearingAllHeadersIsRespected(t *testing.T) {
	if got := mergeRedactedHeaders("{}", `{"Authorization":"Bearer old"}`); got != "{}" {
		t.Fatalf("clearing headers was ignored: %s", got)
	}
}
