package mailboxadapter

import (
	"testing"
)

func TestAgentRevisionsRegistryWrites(t *testing.T) {
	st := newTestStore(t)
	registry := &messagingAgentRegistry{store: st}
	if err := registry.RegisterAgent(t.Context(), "CLI.Host/History", "cli"); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetAgent(t.Context(), "CLI.Host/History")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.GetAgentRevision(t.Context(), current.ID, current.Revision)
	if err != nil || snapshot.Profile.Source != "auto" || snapshot.Profile.Kind != "cli" || snapshot.Profile.ID != current.ID {
		t.Fatalf("registry snapshot = %#v, %v", snapshot, err)
	}
}
