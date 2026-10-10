package service

import (
	"errors"
	"testing"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestRetiredProfileImportPreservesHistoricalContentAndNeverSeedsAuthority(t *testing.T) {
	st := newConfigTestStore(t)
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_profiles(id,name,slug,system_prompt,source) VALUES('history-import','Historical import','history-import','original body','external')`); err != nil {
		t.Fatal(err)
	}
	before, err := st.GetHistoricalAgentProfile(t.Context(), "history-import")
	if err != nil {
		t.Fatal(err)
	}
	def := &agentpkg.Definition{ID: before.ID, Name: "Replaced", Slug: before.Slug, Source: "internal", SystemPrompt: "replacement body", RoleTools: []string{"all"}}
	if err = upsertAgentDef(st, def); !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatal("old import admitted", err)
	}
	for _, created := range []bool{false, true} {
		if err = SeedImportedAgentChildren(st)(t.Context(), before.ID, def, created); !errors.Is(err, store.ErrImmutableAgentProfile) {
			t.Fatal("old child seeds admitted", err)
		}
	}
	AutoIngestAgents(st, []*agentpkg.Definition{def}, nil)
	after, err := st.GetHistoricalAgentProfile(t.Context(), before.ID)
	if err != nil || after.SystemPrompt != before.SystemPrompt || after.Source != before.Source || after.Revision != before.Revision {
		t.Fatalf("retired import changed history: %+v %v", after, err)
	}
	hosts, err := st.ListAgentHostSettings(t.Context())
	if err != nil || len(hosts) != 0 {
		t.Fatal("old import created fresh hosts", err)
	}
}
