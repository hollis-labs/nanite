package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthStatusAndVersion(t *testing.T) {
	dir := gatewayAgent(t) // openai, test-model, key at NANITE_GATEWAY_KEY_REF
	out, err := runRoot(t, dir, "", "auth", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st authStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !st.LoggedIn || st.Provider != "openai" || st.Model != "test-model" || !strings.HasPrefix(st.AuthMethod, "file://") || st.AgentDir != dir {
		t.Fatalf("%+v", st)
	}
	if strings.Contains(out, "unit-test-key") {
		t.Fatal("the key was printed")
	}
	// No key anywhere: loggedIn false with --json (exit 0), an error without.
	t.Setenv("NANITE_GATEWAY_KEY_REF", "file:///nonexistent/nanite-key")
	t.Setenv("NANITE_PROVIDER_KEY_ENV", "")
	out, err = runRoot(t, dir, "", "auth", "status", "--json")
	if err != nil || !strings.Contains(out, `"loggedIn":false`) || !strings.Contains(out, "file:///nonexistent/nanite-key") {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := runRoot(t, dir, "", "auth", "status"); err == nil {
		t.Fatal("no key, no error")
	}
	if out, err := runRoot(t, dir, "", "--version"); err != nil || !strings.HasSuffix(out, "(Nanite)\n") {
		t.Fatalf("version: %v %q", err, out)
	}
}
