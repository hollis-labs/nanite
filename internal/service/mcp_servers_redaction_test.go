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

func TestRedactEnv(t *testing.T) {
	p := RedactedHeaderValue
	for _, c := range []struct{ name, in, want string }{
		{"empty string", "", ""},
		{"empty list", "[]", "[]"},
		{"unparseable", `["A=1"`, "[]"},
		{"values hidden, keys and order kept", `["B=2","A=1","C=x=y"]`, `["B=` + p + `","A=` + p + `","C=` + p + `"]`},
		{"no = is a bare placeholder", `["ghp_pasted_token"]`, `["` + p + `"]`},
		{"empty value still hidden", `["K="]`, `["K=` + p + `"]`},
	} {
		if got := RedactEnv(c.in); got != c.want {
			t.Errorf("%s: RedactEnv(%s) = %s, want %s", c.name, c.in, got, c.want)
		}
	}
}

func TestMergeRedactedEnv(t *testing.T) {
	p := RedactedHeaderValue
	stored := `["TOKEN=old-first","DEBUG=1","TOKEN=real","ghp_bare"]`
	for _, c := range []struct{ name, in, stored, want string }{
		{"placeholder restored from the last stored entry", `["TOKEN=` + p + `"]`, stored, `["TOKEN=real"]`},
		{"invented key dropped", `["NEW=` + p + `","DEBUG=1"]`, stored, `["DEBUG=1"]`},
		{"bare placeholder dropped", `["` + p + `","DEBUG=1"]`, stored, `["DEBUG=1"]`},
		{"real edit kept", `["TOKEN=new"]`, stored, `["TOKEN=new"]`},
		{"explicit empty kept", `["TOKEN="]`, stored, `["TOKEN="]`},
		{"order preserved", `["DEBUG=2","TOKEN=` + p + `","A=1"]`, stored, `["DEBUG=2","TOKEN=real","A=1"]`},
		{"nothing redacted returns input as sent", `[ "DEBUG=2" ]`, stored, `[ "DEBUG=2" ]`},
		{"unparseable incoming passes through", `["A=1"`, stored, `["A=1"`},
		{"unparseable stored drops placeholders", `["TOKEN=` + p + `","A=1"]`, `not json`, `["A=1"]`},
		{"empty stored drops placeholders", `["TOKEN=` + p + `"]`, "", `[]`},
	} {
		if got := MergeRedactedEnv(c.in, c.stored); got != c.want {
			t.Errorf("%s: MergeRedactedEnv(%s, %s) = %s, want %s", c.name, c.in, c.stored, got, c.want)
		}
	}
}

// With nothing parseable stored, a header placeholder has no value to keep
// and is dropped, not stored as bullets.
func TestMergeRedactedHeaders_dropsPlaceholdersWhenStoredIsUnusable(t *testing.T) {
	incoming := `{"Authorization":"` + RedactedHeaderValue + `","X-Keep":"1"}`
	for _, stored := range []string{"", "{}", `{"Authorization":"Bearer oops`} {
		if got := MergeRedactedHeaders(incoming, stored); got != `{"X-Keep":"1"}` {
			t.Errorf("stored %q: got %s, want only X-Keep", stored, got)
		}
	}
}
