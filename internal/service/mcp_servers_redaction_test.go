package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactHeaders_keepsKeysAndDropsValues(t *testing.T) {
	out := RedactHeaders(`{"Authorization":"Bearer supersecret","X-Tenant":"acme-corp"}`)

	if strings.Contains(out, "supersecret") {
		t.Fatalf("token survived redaction: %s", out)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("redacted headers are not valid json: %v", err)
	}
	for _, k := range []string{"Authorization", "X-Tenant"} {
		if got[k] != RedactedHeaderValue {
			t.Fatalf("key %q: got %q, want the redaction marker", k, got[k])
		}
	}
}

func TestRedactHeaders_hidesUnparseableValuesToo(t *testing.T) {
	if out := RedactHeaders(`{"Authorization":"Bearer oops`); out != "{}" {
		t.Fatalf("unparseable headers not replaced with {}: %s", out)
	}
	if out := RedactHeaders(""); out != "{}" {
		t.Fatalf("empty headers: got %s, want {}", out)
	}
}

func TestMergeRedactedHeaders_restoresUnchangedSecrets(t *testing.T) {
	stored := `{"Authorization":"Bearer supersecret","X-Tenant":"acme-corp"}`
	// What a UI sends back after loading the redacted list and editing nothing.
	incoming := `{"Authorization":"` + RedactedHeaderValue + `","X-Tenant":"` + RedactedHeaderValue + `"}`

	merged := MergeRedactedHeaders(incoming, stored)

	var got map[string]string
	if err := json.Unmarshal([]byte(merged), &got); err != nil {
		t.Fatalf("merged headers are not valid json: %v", err)
	}
	if got["Authorization"] != "Bearer supersecret" {
		t.Fatalf("token was not restored: %q", got["Authorization"])
	}
	if got["X-Tenant"] != "acme-corp" {
		t.Fatalf("second header was not restored: %q", got["X-Tenant"])
	}
}

func TestMergeRedactedHeaders_letsRealEditsThrough(t *testing.T) {
	stored := `{"Authorization":"Bearer old"}`
	incoming := `{"Authorization":"Bearer new"}`
	merged := MergeRedactedHeaders(incoming, stored)
	if !strings.Contains(merged, "Bearer new") {
		t.Fatalf("a deliberate change was discarded: %s", merged)
	}
}

func TestMergeRedactedHeaders_dropsARedactedKeyThatNeverExisted(t *testing.T) {
	// A client inventing a key with the marker as its value has given us no
	// value at all; storing bullets would be worse than storing nothing.
	stored := `{"Authorization":"Bearer old"}`
	incoming := `{"Authorization":"Bearer old","X-Invented":"` + RedactedHeaderValue + `"}`
	merged := MergeRedactedHeaders(incoming, stored)
	if strings.Contains(merged, "X-Invented") {
		t.Fatalf("invented redacted key was stored: %s", merged)
	}
}

func TestMergeRedactedHeaders_clearingAllHeadersIsRespected(t *testing.T) {
	if got := MergeRedactedHeaders("{}", `{"Authorization":"Bearer old"}`); got != "{}" {
		t.Fatalf("clearing headers was ignored: %s", got)
	}
}
