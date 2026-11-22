package store

import (
	"errors"
	"testing"
)

func TestHostProtocolTransportRemainHostOwned(t *testing.T) {
	s := newTestStore(t)
	h := makeTestHost(t, s, "host-protocol")
	h.Settings.Runtime = "cli"
	h.Settings.Provider = "opencode"
	h.Settings.Protocol = "acp"
	h.Settings.Transport = "stdio"
	saved, err := s.UpdateAgentHostSettings(t.Context(), h, h.Revision)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := s.GetAgent(t.Context(), h.ID)
	if err != nil || projection.Protocol != "acp" || projection.Transport != "stdio" || projection.NativeHost == nil {
		t.Fatal(projection, err)
	}
	saved.Settings.Protocol = ""
	saved.Settings.Transport = "tcp"
	if _, err = s.UpdateAgentHostSettings(t.Context(), saved, saved.Revision); err == nil {
		t.Fatal("transport without protocol accepted")
	}
	retained, err := s.GetAgentHostSettings(t.Context(), h.ID)
	if err != nil || retained.Revision != saved.Revision || retained.Settings.Transport != "stdio" {
		t.Fatal(retained, err)
	}
	protocol, transport := "acp", "tcp"
	if err = s.UpdateAgentACPConfig(t.Context(), h.ID, &protocol, &transport); !errors.Is(err, ErrImmutableAgentProfile) {
		t.Fatal("legacy protocol writer admitted", err)
	}
}
