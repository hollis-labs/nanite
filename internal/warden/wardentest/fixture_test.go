package wardentest

import (
	"encoding/json"
	"strings"
	"testing"
)

type probe struct {
	testing.TB
	failed bool
}

func (p *probe) Helper()               {}
func (p *probe) Fatalf(string, ...any) { p.failed = true }

func TestCheckCleanRefusesPrivateShapes(t *testing.T) {
	for _, s := range []string{
		"https://warden.example.test", "warden.nanite.co", "gw.internal", "warden-prod:8443",
		"192.0.2.7", "2001:db8:0:0:1:0:0:1", "fe80::1", "Bearer abc", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig",
	} {
		p := &probe{TB: t}
		CheckClean(p, "probe", Fixture{Response: Response{Body: json.RawMessage(`{"error_description":"` + s + `"}`)}})
		if !p.failed {
			t.Errorf("CheckClean passed %q", s)
		}
	}
	for _, s := range []string{"llm-gateway.chat", "2026-11-21T11:36:13Z", "warden.request warden.read"} {
		p := &probe{TB: t}
		CheckClean(p, "probe", Fixture{Response: Response{Body: json.RawMessage(`{"scope":"` + s + `"}`)}})
		if p.failed {
			t.Errorf("CheckClean refused %q", s)
		}
	}
}

func TestScrubReachesNestedFields(t *testing.T) {
	out := string(scrubBody([]byte(`{"data":{"access_token":"secret-a","items":[{"refresh_token":"secret-b"}]}}`)))
	for _, secret := range []string{"secret-a", "secret-b"} {
		if strings.Contains(out, secret) {
			t.Fatalf("scrubbed body keeps %s: %s", secret, out)
		}
	}
}
