package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/libs/util/sqlite/sqlitekit"
	"github.com/pressly/goose/v3"
)

// This fixture runs the real migrations through the last historical schema,
// records operator-owned history, and lets New perform the fresh partition cut.
// It changes neither migration text nor the production migration ledger.
func partitionHistoricalStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "historical.db")
	db, err := sqlitekit.OpenSingle(ctx, path, sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 176); err != nil {
		t.Fatal(err)
	}
	return &Store{DB: db, dbPath: path}
}

func partitionExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func partitionHost(t *testing.T, s *Store, slug string) AgentHostSettings {
	t.Helper()
	ctx := context.Background()
	pin, err := s.InstallAgentDefinition(ctx, definitionTestBytes("def:partition-behavior", "1", "Fresh explicitly authored instructions."), nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.CreateAgentHostSettings(ctx, AgentHostSettings{
		Slug: slug, Title: "Fresh host", DefinitionRef: pin,
		Settings: NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture-provider", Model: "fixture-model"},
		Enabled:  true, Source: "explicit-private-test-authoring",
	})
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func partitionNoAuthority(t *testing.T, s *Store, identity string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.GetAgentForActor(ctx, identity); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatalf("unbound identity admitted as an actor: %v", err)
	}
	for _, read := range []struct {
		name string
		get  func(context.Context, string) ([]string, error)
	}{{"tool grants", s.ListAgentToolNames}, {"dispatch grants", s.ListAgentDispatchToolNames}} {
		names, err := read.get(ctx, identity)
		if err != nil || len(names) != 0 {
			t.Fatalf("%s exposed historical authority: %v, %v", read.name, names, err)
		}
	}
	skills, err := s.ListAgentKnownSkills(ctx, identity)
	if err != nil || len(skills) != 0 {
		t.Fatalf("historical skill approval became actor authority: %+v, %v", skills, err)
	}
	known, err := s.ListAgentKnownTools(ctx, identity)
	if err != nil || len(known) != 0 {
		t.Fatalf("historical familiar tools became actor state: %+v, %v", known, err)
	}
	if err = s.GrantAgentTool(ctx, identity, "historical-tool", "explicit"); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatalf("grant issuance without a verified issuer accepted: %v", err)
	}
	if err = s.AssignSkillToAgent(ctx, identity, "historical-skill", "claimed-operator"); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatalf("skill issuance without a verified issuer accepted: %v", err)
	}
	if _, err = s.ResolveTrust(ctx, identity); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatalf("historical trust accepted: %v", err)
	}
}

func TestAgentPartitionUpgradePreservesHistoryWithoutAuthority(t *testing.T) {
	old := partitionHistoricalStore(t)
	ctx := context.Background()
	// A syntactically valid actor URI in old columns is still only history.
	const historicalID = "msg://agent/private-fixture/historical"
	partitionExec(t, old, `INSERT INTO agent_profiles(id,name,slug,system_prompt,source,tools,settings) VALUES(?,?,?,?,?,?,?)`, historicalID, "Historical", "historical-host", "Historical prompt must remain audited.", "user", `["historical.admin"]`, `{"legacy_setting":"retained"}`)
	partitionExec(t, old, `INSERT INTO known_tools(id,name,source,status,created_at,updated_at) VALUES('historical-tool','historical.admin','builtin','available','fixture-time','fixture-time')`)
	partitionExec(t, old, `INSERT INTO agent_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,'historical-tool','explicit','fixture-time')`, historicalID)
	partitionExec(t, old, `INSERT INTO agent_dispatch_tool_allowlist(agent_id,tool_id,created_at) VALUES(?,'historical-tool','fixture-time')`, historicalID)
	partitionExec(t, old, `INSERT INTO agent_known_tools(agent_id,tool_name,pinned,reason) VALUES(?,'historical.admin',1,'old operator selection')`, historicalID)
	partitionExec(t, old, `INSERT INTO agent_known_skills(agent_id,skill_name,approved_content_hash,granted_by,capabilities_granted) VALUES(?,'historical-skill','retained-approved-hash','old-operator','{"write":true}')`, historicalID)
	partitionExec(t, old, `INSERT INTO sessions(id,short_code,title) VALUES('historical-session','hist01','Historical session')`)
	partitionExec(t, old, `INSERT INTO session_agents(session_id,agent_id,mode,is_primary) VALUES('historical-session',?,'default',1)`, historicalID)
	before, err := old.GetHistoricalAgentProfile(ctx, historicalID)
	if err != nil {
		t.Fatal(err)
	}
	path := old.DBPath(ctx)
	if err = old.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	after, err := s.GetHistoricalAgentProfile(ctx, historicalID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("fresh migration changed audited profile: before=%+v after=%+v error=%v", before, after, err)
	}
	if _, err = s.GetAgent(ctx, historicalID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ordinary lookup fell back to history: %v", err)
	}
	partitionNoAuthority(t, s, historicalID)
	bindings, err := s.ListSessionAgents(ctx, "historical-session")
	if err != nil || len(bindings) != 0 {
		t.Fatalf("historical session membership enrolled actor: %+v, %v", bindings, err)
	}
	// Explicit reuse of a historical name still cannot reuse its identity/grants.
	host := partitionHost(t, s, "historical-host")
	projection, err := s.GetAgentBySlug(ctx, "historical-host")
	if err != nil || projection.ID != host.ID || projection.ID == historicalID || projection.SystemPrompt == before.SystemPrompt {
		t.Fatalf("fresh slug projection inherited historical identity/content: %+v, %v", projection, err)
	}
	partitionNoAuthority(t, s, host.ID)
	host.Title = "Host-only replacement"
	host, err = s.UpdateAgentHostSettings(ctx, host, host.Revision)
	if err != nil {
		t.Fatal(err)
	}
	partitionNoAuthority(t, s, host.ID)
	// The original graph remains readable for audit after migration and authoring.
	var grantVia, approvedHash, grantedBy, capabilities, mode string
	if err = s.DB.QueryRowContext(ctx, `SELECT granted_via FROM agent_tools WHERE agent_id=? AND tool_id='historical-tool'`, historicalID).Scan(&grantVia); err != nil || grantVia != "explicit" {
		t.Fatalf("historical tool grant altered: %q, %v", grantVia, err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT approved_content_hash,granted_by,capabilities_granted FROM agent_known_skills WHERE agent_id=? AND skill_name='historical-skill'`, historicalID).Scan(&approvedHash, &grantedBy, &capabilities); err != nil || approvedHash != "retained-approved-hash" || grantedBy != "old-operator" || capabilities != `{"write":true}` {
		t.Fatalf("historical approved skill altered: %q %q %q, %v", approvedHash, grantedBy, capabilities, err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT mode FROM session_agents WHERE session_id='historical-session' AND agent_id=?`, historicalID).Scan(&mode); err != nil || mode != "default" {
		t.Fatalf("historical membership altered: %q, %v", mode, err)
	}
	var minted bool
	if err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_actor_bindings)`).Scan(&minted); err != nil || minted {
		t.Fatalf("migration or host authoring minted an actor: %v, %v", minted, err)
	}
}

func TestAgentPartitionHostRevisionCompetingWriters(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host := partitionHost(t, s, "competing-host")
	artifactBefore, err := s.GetAgentDefinitionArtifact(ctx, host.DefinitionRef)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(ctx, s.DBPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close(context.Background()) })
	type outcome struct {
		settings AgentHostSettings
		err      error
	}
	start, results := make(chan struct{}), make(chan outcome, 2)
	for index, writer := range []*Store{s, second} {
		input := host
		if index == 0 {
			input.Title, input.Settings.Model = "Writer one", "one-model"
		} else {
			input.Title, input.Settings.Model = "Writer two", "two-model"
		}
		go func(store *Store, replacement AgentHostSettings) {
			<-start
			updated, updateErr := store.UpdateAgentHostSettings(ctx, replacement, host.Revision)
			results <- outcome{settings: updated, err: updateErr}
		}(writer, input)
	}
	close(start)
	var winner AgentHostSettings
	succeeded, conflicted := 0, 0
	for range 2 {
		select {
		case result := <-results:
			switch {
			case result.err == nil:
				succeeded++
				winner = result.settings
			case errors.Is(result.err, ErrAgentHostRevisionConflict):
				conflicted++
			default:
				t.Fatalf("competing writer failed outside CAS: %v", result.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if succeeded != 1 || conflicted != 1 || winner.Revision == host.Revision {
		t.Fatalf("CAS winner/conflict=%d/%d, revision=%s", succeeded, conflicted, winner.Revision)
	}
	stored, err := s.GetAgentHostSettings(ctx, host.ID)
	if err != nil || !reflect.DeepEqual(stored, winner) {
		t.Fatalf("mixed or missing host replacement: got=%+v winner=%+v error=%v", stored, winner, err)
	}
	if _, err = s.UpdateAgentHostSettings(ctx, host, host.Revision); !errors.Is(err, ErrAgentHostRevisionConflict) {
		t.Fatalf("stale update overwrote winner: %v", err)
	}
	if err = s.DeleteAgentHostSettings(ctx, host.ID, host.Revision); !errors.Is(err, ErrAgentHostRevisionConflict) {
		t.Fatalf("stale deletion removed winner: %v", err)
	}
	artifactAfter, err := s.GetAgentDefinitionArtifact(ctx, host.DefinitionRef)
	if err != nil || !reflect.DeepEqual(artifactBefore, artifactAfter) {
		t.Fatalf("host writers changed immutable content: %v", err)
	}
	partitionNoAuthority(t, s, host.ID)
}

func TestAgentPartitionAdmissionNeedsEnabledVerifiedBinding(t *testing.T) {
	for _, state := range []struct {
		name                  string
		binding, actorEnabled bool
		hostEnabled           bool
		receipt               string
		admit                 bool
	}{
		{name: "absent binding", hostEnabled: true},
		{name: "empty verification receipt", binding: true, actorEnabled: true, hostEnabled: true},
		{name: "disabled actor", binding: true, hostEnabled: true, receipt: "private-host-authorized-simulation"},
		{name: "disabled host", binding: true, actorEnabled: true, receipt: "private-host-authorized-simulation"},
		{name: "existing host-authorized simulation", binding: true, actorEnabled: true, hostEnabled: true, receipt: "private-host-authorized-simulation", admit: true},
	} {
		t.Run(state.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			host := partitionHost(t, s, "admission-host")
			const actor = "msg://agent/private-fixture/operational"
			partitionExec(t, s, `INSERT INTO sessions(id,short_code,title) VALUES('operational-session','oper01','Private operational fixture')`)
			partitionExec(t, s, `UPDATE agent_host_settings SET enabled=? WHERE id=?`, state.hostEnabled, host.ID)
			if state.binding {
				// Private trusted-DB simulation of an already host-authorized binding,
				// never enrollment, receipt verification or a production issuer.
				partitionExec(t, s, `INSERT INTO agent_actor_bindings(actor_uri,host_settings_id,binding_receipt,enabled) VALUES(?,?,?,?)`, actor, host.ID, state.receipt, state.actorEnabled)
			}
			resolved, err := s.GetAgentForActor(ctx, actor)
			if state.admit {
				if err != nil || resolved.ID != actor || resolved.Slug != host.Slug {
					t.Fatalf("existing authorized binding unreadable: %+v, %v", resolved, err)
				}
			} else if !errors.Is(err, ErrVerifiedActorRequired) {
				t.Fatalf("actor read admitted invalid binding: %v", err)
			}
			err = s.EnsureSessionAgent(ctx, "operational-session", actor, "default", true)
			if (err == nil) != state.admit {
				t.Fatalf("session admission error=%v, want admitted=%v", err, state.admit)
			}
			err = s.InsertAgentKnownTool(ctx, AgentKnownTool{AgentID: actor, ToolName: "fixture-known-request"})
			if (err == nil) != state.admit {
				t.Fatalf("operational tool state admission error=%v, want admitted=%v", err, state.admit)
			}
			// Even a readable binding does not provide a grant issuer.
			if err = s.GrantAgentTool(ctx, actor, "fixture-tool", "explicit"); !errors.Is(err, ErrVerifiedActorRequired) {
				t.Fatalf("binding reader became grant issuance: %v", err)
			}
		})
	}
}

func TestAgentPartitionDisableRechecksOperationalReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host := partitionHost(t, s, "disable-host")
	const actor = "msg://agent/private-fixture/disable"
	// Only this private DB fixture simulates an existing host-authorized receipt.
	partitionExec(t, s, `INSERT INTO agent_actor_bindings(actor_uri,host_settings_id,binding_receipt) VALUES(?,?,'private-host-authorized-simulation')`, actor, host.ID)
	partitionExec(t, s, `INSERT INTO sessions(id,short_code,title) VALUES('disable-session','disa01','Private disable fixture')`)
	if err := s.EnsureSessionAgent(ctx, "disable-session", actor, "default", true); err != nil {
		t.Fatal(err)
	}
	known := AgentKnownTool{AgentID: actor, ToolName: "fixture-known-request", Reason: "before disabling"}
	if err := s.InsertAgentKnownTool(ctx, known); err != nil {
		t.Fatal(err)
	}
	partitionExec(t, s, `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`, actor)
	known.Reason = "must not replace"
	if err := s.InsertAgentKnownTool(ctx, known); err == nil {
		t.Fatal("cached actor reference replaced operational state after disabling")
	}
	if err := s.EnsureSessionAgent(ctx, "disable-session", actor, "must-not-replace", false); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatalf("disabled binding changed existing membership: %v", err)
	}
	var reason, mode string
	if err := s.DB.QueryRowContext(ctx, `SELECT reason FROM actor_known_tools WHERE agent_id=? AND tool_name=?`, actor, known.ToolName).Scan(&reason); err != nil || reason != "before disabling" {
		t.Fatalf("failed replacement changed accepted state: %q, %v", reason, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT mode FROM session_actor_bindings WHERE session_id='disable-session' AND agent_id=?`, actor).Scan(&mode); err != nil || mode != "default" {
		t.Fatalf("failed membership update changed accepted state: %q, %v", mode, err)
	}
}
