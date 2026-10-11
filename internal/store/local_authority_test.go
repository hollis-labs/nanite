package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func nativeHost(t *testing.T, s *Store, slug string) AgentHostSettings {
	t.Helper()
	ctx := context.Background()
	pin, err := s.InstallAgentDefinition(ctx, definitionTestBytes("def:native-"+slug, "1", "Native authority fixture "+slug+"."), nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.CreateAgentHostSettings(ctx, AgentHostSettings{Slug: slug, Title: slug, DefinitionRef: pin,
		Settings: NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture-provider", Model: "fixture-model"},
		Enabled:  true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func nativeTool(t *testing.T, s *Store, name string) {
	t.Helper()
	if _, err := s.UpsertKnownTool(context.Background(), name, "builtin", "available", name); err != nil {
		t.Fatal(err)
	}
}

func authorityRowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// --- negative cases first -------------------------------------------------

func TestLocalAuthorityRefusesWithoutAGrantingCredential(t *testing.T) {
	s := newTestStore(t)
	host := nativeHost(t, s, "needs-credential")
	la := s.LocalAuthority()
	for _, who := range []string{"", "   "} {
		if _, err := la.CreateAuthority(context.Background(), who, host.ID, "r"); !errors.Is(err, ErrNativeAuthorityActor) {
			t.Fatalf("create with grantedBy %q: %v", who, err)
		}
	}
	if authorityRowCount(t, s, "native_authorities") != 0 || authorityRowCount(t, s, "native_authority_audit") != 0 {
		t.Fatal("a refused create left rows behind")
	}
}

func TestLocalAuthorityRefusesUnknownOrDisabledHostSettings(t *testing.T) {
	s := newTestStore(t)
	la := s.LocalAuthority()
	if _, err := la.CreateAuthority(context.Background(), "cred-1", "no-such-host", "r"); !errors.Is(err, ErrNativeAuthorityNotFound) {
		t.Fatalf("unknown host: %v", err)
	}
	host := nativeHost(t, s, "disabled-host")
	host.Enabled = false
	if _, err := s.UpdateAgentHostSettings(context.Background(), host, host.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := la.CreateAuthority(context.Background(), "cred-1", host.ID, "r"); err == nil {
		t.Fatal("authority created for disabled host settings")
	}
}

func TestNativeAuthorityTriggersCheckShapeAndAreAppendOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host := nativeHost(t, s, "triggers")
	nativeTool(t, s, "dev_read")
	la := s.LocalAuthority()
	a, err := la.CreateAuthority(ctx, "cred-1", host.ID, "r")
	if err != nil {
		t.Fatal(err)
	}
	var toolID string
	if err = s.DB.QueryRowContext(ctx, `SELECT id FROM known_tools WHERE name='dev_read'`).Scan(&toolID); err != nil {
		t.Fatal(err)
	}
	for name, stmt := range map[string]string{
		"wrong authority in an authority receipt":    `INSERT INTO native_authorities(id,host_settings_id,host_settings_revision,definition_id,definition_revision,semantic_digest,receipt) VALUES('x','` + host.ID + `','r2','` + a.DefinitionID + `','` + a.DefinitionRevision + `','` + a.SemanticDigest + `','{"authority":"environment","granted_by":"c"}')`,
		"missing granted_by in an authority receipt": `INSERT INTO native_authorities(id,host_settings_id,host_settings_revision,definition_id,definition_revision,semantic_digest,receipt) VALUES('x','` + host.ID + `','r3','` + a.DefinitionID + `','` + a.DefinitionRevision + `','` + a.SemanticDigest + `','{"authority":"nanite-local"}')`,
		"wrong authority in a tool receipt":          `INSERT INTO native_authority_tools(authority_id,tool_id,receipt) VALUES('` + a.ID + `','` + toolID + `','{"authority":"tether","granted_by":"c"}')`,
		"audit update":                               `UPDATE native_authority_audit SET reason='edited'`,
		"audit delete":                               `DELETE FROM native_authority_audit`,
		"pin change":                                 `UPDATE native_authorities SET host_settings_revision='other' WHERE id='` + a.ID + `'`,
		"receipt rewrite":                            `UPDATE native_authorities SET receipt='{"authority":"nanite-local","granted_by":"someone-else"}' WHERE id='` + a.ID + `'`,
	} {
		if _, err := s.DB.ExecContext(ctx, stmt); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err = la.RevokeAuthority(ctx, "cred-1", a.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE native_authorities SET enabled=1, revoked_at=NULL WHERE id=?`, a.ID); err == nil {
		t.Error("a revoked authority was revived")
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO native_authority_tools(authority_id,tool_id,receipt) VALUES(?,?,?)`, a.ID, toolID, `{"authority":"nanite-local","granted_by":"c"}`); err == nil {
		t.Error("a tool was granted to a revoked authority")
	}
}

func TestLocalAuthorityDoesNotTouchFabricAuthority(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host := nativeHost(t, s, "fabric-untouched")
	nativeTool(t, s, "dev_read")
	la := s.LocalAuthority()
	a, err := la.CreateAuthority(ctx, "cred-1", host.ID, "r")
	if err != nil {
		t.Fatal(err)
	}
	if err = la.GrantTool(ctx, "cred-1", a.ID, "dev_read", "r"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"agent_actor_bindings", "actor_granted_tools", "actor_known_tools", "actor_known_skills"} {
		if n := authorityRowCount(t, s, table); n != 0 {
			t.Errorf("%s has %d rows after local authority writes", table, n)
		}
	}
	var toolID string
	_ = s.DB.QueryRowContext(ctx, `SELECT id FROM known_tools WHERE name='dev_read'`).Scan(&toolID)
	if err = s.GrantAgentTool(ctx, a.ID, toolID, "explicit"); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatalf("the fabric grant path stopped refusing: %v", err)
	}
	for _, id := range []string{a.ID, host.ID, "nanite-local:host-settings/" + host.ID} {
		if names, lerr := s.ListAgentToolNames(ctx, id); lerr != nil || len(names) != 0 {
			t.Errorf("fabric tool read answered for %q: %v %v", id, names, lerr)
		}
	}
}

func TestLocalAuthorityGrantRules(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host := nativeHost(t, s, "grant-rules")
	nativeTool(t, s, "dev_read")
	if _, err := s.UpsertKnownTool(ctx, "gone_tool", "builtin", "removed", "x"); err != nil {
		t.Fatal(err)
	}
	la := s.LocalAuthority()
	a, err := la.CreateAuthority(ctx, "cred-1", host.ID, "r")
	if err != nil {
		t.Fatal(err)
	}
	if err = la.GrantTool(ctx, "cred-1", a.ID, "no_such_tool", "r"); !errors.Is(err, ErrNativeToolNotGrantable) {
		t.Fatalf("unknown tool: %v", err)
	}
	if err = la.GrantTool(ctx, "cred-1", a.ID, "gone_tool", "r"); !errors.Is(err, ErrNativeToolNotGrantable) {
		t.Fatalf("unavailable tool: %v", err)
	}
	if err = la.GrantTool(ctx, " ", a.ID, "dev_read", "r"); !errors.Is(err, ErrNativeAuthorityActor) {
		t.Fatalf("grant without credential: %v", err)
	}
	if err = la.GrantTool(ctx, "cred-1", "no-such-authority", "dev_read", "r"); !errors.Is(err, ErrNativeAuthorityNotFound) {
		t.Fatalf("unknown authority: %v", err)
	}
	if tools, _ := la.ListTools(ctx, a.ID); len(tools) != 0 {
		t.Fatalf("a refused grant left a tool: %v", tools)
	}
	if err = la.RevokeAuthority(ctx, "cred-1", a.ID, "r"); err != nil {
		t.Fatal(err)
	}
	if err = la.GrantTool(ctx, "cred-1", a.ID, "dev_read", "r"); !errors.Is(err, ErrNativeAuthorityRevoked) {
		t.Fatalf("grant to a revoked authority: %v", err)
	}
}

// --- positive paths ---------------------------------------------------------

func TestLocalAuthorityLifecycleAuditsEveryChangeAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host := nativeHost(t, s, "lifecycle")
	nativeTool(t, s, "dev_read")
	nativeTool(t, s, "dev_grep")
	la := s.LocalAuthority()
	a, err := la.CreateAuthority(ctx, "cred-1", host.ID, "first review")
	if err != nil {
		t.Fatal(err)
	}
	if a.HostSettingsRevision != host.Revision || a.SemanticDigest != host.DefinitionRef.Digest || !a.Enabled {
		t.Fatalf("authority did not record the reviewed revision and pin: %+v", a)
	}
	if _, err = la.CreateAuthority(ctx, "cred-1", host.ID, "again"); !errors.Is(err, ErrNativeAuthorityExists) {
		t.Fatalf("second live authority: %v", err)
	}
	for _, tool := range []string{"dev_read", "dev_grep", "dev_read"} { // the repeat is a no-op
		if err = la.GrantTool(ctx, "cred-2", a.ID, tool, "workspace helper"); err != nil {
			t.Fatal(err)
		}
	}
	tools, err := la.ListTools(ctx, a.ID)
	if err != nil || len(tools) != 2 || tools[0].Name != "dev_grep" || tools[1].Name != "dev_read" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	var receiptBy string
	if err = s.DB.QueryRowContext(ctx, `SELECT json_extract(receipt,'$.granted_by') FROM native_authority_tools LIMIT 1`).Scan(&receiptBy); err != nil || receiptBy != "cred-2" {
		t.Fatalf("receipt granted_by = %q, %v", receiptBy, err)
	}
	if err = la.RevokeTool(ctx, "cred-1", a.ID, "dev_grep", "not needed"); err != nil {
		t.Fatal(err)
	}
	if err = la.RevokeTool(ctx, "cred-1", a.ID, "dev_grep", "again"); err != nil { // no-op
		t.Fatal(err)
	}
	if err = la.RevokeAuthority(ctx, "cred-1", a.ID, "retire"); err != nil {
		t.Fatal(err)
	}
	if err = la.RevokeAuthority(ctx, "cred-1", a.ID, "twice"); !errors.Is(err, ErrNativeAuthorityNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT action FROM native_authority_audit WHERE authority_id=? ORDER BY id`, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRows(rows)
	var got []string
	for rows.Next() {
		var act string
		_ = rows.Scan(&act)
		got = append(got, act)
	}
	want := "authority_created,tool_granted,tool_granted,tool_revoked,authority_revoked"
	if strings.Join(got, ",") != want {
		t.Fatalf("audit = %v, want %s", got, want)
	}
	// A new review of the same revision may start fresh after a revoke.
	b, err := la.CreateAuthority(ctx, "cred-1", host.ID, "re-review")
	if err != nil || b.ID == a.ID {
		t.Fatalf("re-review: %+v, %v", b, err)
	}
	if tools, _ = la.ListTools(ctx, b.ID); len(tools) != 0 {
		t.Fatalf("a new authority inherited grants: %v", tools)
	}
}

func TestLocalAuthoritiesAreIsolatedAndPinnedToWhatWasReviewed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	h1 := nativeHost(t, s, "view-a")
	h2 := nativeHost(t, s, "view-b")
	nativeTool(t, s, "dev_read")
	la := s.LocalAuthority()
	a, _ := la.CreateAuthority(ctx, "cred-1", h1.ID, "r")
	b, _ := la.CreateAuthority(ctx, "cred-1", h2.ID, "r")
	if err := la.GrantTool(ctx, "cred-1", a.ID, "dev_read", "r"); err != nil {
		t.Fatal(err)
	}
	if tools, _ := la.ListTools(ctx, b.ID); len(tools) != 0 {
		t.Fatalf("a grant for A appears for B: %v", tools)
	}
	// Changing the host settings does not rewrite what was reviewed.
	h1.Settings.Model = "another-model"
	updated, err := s.UpdateAgentHostSettings(ctx, h1, h1.Revision)
	if err != nil {
		t.Fatal(err)
	}
	again, err := la.GetAuthority(ctx, a.ID)
	if err != nil || again.HostSettingsRevision != h1.Revision || again.HostSettingsRevision == updated.Revision {
		t.Fatalf("authority revision moved with the settings: %+v (settings now %s)", again, updated.Revision)
	}
}

// --- resolver (slice 2) -----------------------------------------------------

func resolveFixture(t *testing.T) (*Store, AgentHostSettings, NativeAuthority) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	host := nativeHost(t, s, "resolver")
	nativeTool(t, s, "dev_read")
	nativeTool(t, s, "dev_grep")
	la := s.LocalAuthority()
	a, err := la.CreateAuthority(ctx, "cred-1", host.ID, "r")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"dev_read", "dev_grep"} {
		if err = la.GrantTool(ctx, "cred-1", a.ID, tool, "r"); err != nil {
			t.Fatal(err)
		}
	}
	return s, host, a
}

func TestResolveToolNamesYieldsNothingUnlessTheReviewedStateIsStillInForce(t *testing.T) {
	ctx := context.Background()
	s, host, a := resolveFixture(t)
	la := s.LocalAuthority()
	names, err := la.ResolveToolNames(ctx, host.ID, host.Revision)
	if err != nil || strings.Join(names, ",") != "dev_grep,dev_read" {
		t.Fatalf("baseline = %v, %v", names, err)
	}
	if names, _ = la.ResolveToolNames(ctx, "some-other-host", host.Revision); len(names) != 0 {
		t.Fatalf("another host resolved grants: %v", names)
	}
	// A tool that becomes unavailable stops resolving.
	if _, err = s.UpsertKnownTool(ctx, "dev_grep", "builtin", "removed", "x"); err != nil {
		t.Fatal(err)
	}
	if names, _ = la.ResolveToolNames(ctx, host.ID, host.Revision); strings.Join(names, ",") != "dev_read" {
		t.Fatalf("unavailable tool still resolved: %v", names)
	}
	if _, err = s.UpsertKnownTool(ctx, "dev_grep", "builtin", "available", "x"); err != nil {
		t.Fatal(err)
	}
	// A revoked tool grant stops resolving.
	if err = la.RevokeTool(ctx, "cred-1", a.ID, "dev_read", "r"); err != nil {
		t.Fatal(err)
	}
	if names, _ = la.ResolveToolNames(ctx, host.ID, host.Revision); strings.Join(names, ",") != "dev_grep" {
		t.Fatalf("revoked grant still resolved: %v", names)
	}
	// Settings changed since the review: the old authority no longer applies.
	host.Settings.Model = "changed"
	if _, err = s.UpdateAgentHostSettings(ctx, host, host.Revision); err != nil {
		t.Fatal(err)
	}
	if names, _ = la.ResolveToolNames(ctx, host.ID, host.Revision); len(names) != 0 {
		t.Fatalf("grants survived a settings revision change: %v", names)
	}
}

func TestResolveToolNamesStopsOnRevokedAuthorityAndDisabledHost(t *testing.T) {
	ctx := context.Background()
	s, host, a := resolveFixture(t)
	la := s.LocalAuthority()
	if err := la.RevokeAuthority(ctx, "cred-1", a.ID, "r"); err != nil {
		t.Fatal(err)
	}
	if names, _ := la.ResolveToolNames(ctx, host.ID, host.Revision); len(names) != 0 {
		t.Fatalf("revoked authority resolved: %v", names)
	}
	s2, host2, _ := resolveFixture(t)
	host2.Enabled = false
	if _, err := s2.UpdateAgentHostSettings(ctx, host2, host2.Revision); err != nil {
		t.Fatal(err)
	}
	if names, _ := s2.LocalAuthority().ResolveToolNames(ctx, host2.ID, host2.Revision); len(names) != 0 {
		t.Fatalf("disabled host resolved: %v", names)
	}
}

func TestResolveToolNamesRequiresTheViewToHaveBeenAdmittedUnderTheReviewedRevision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	host := nativeHost(t, s, "admitted")
	nativeTool(t, s, "dev_read")
	older := host.Revision
	host.Settings.Model = "second-revision"
	updated, err := s.UpdateAgentHostSettings(ctx, host, older)
	if err != nil {
		t.Fatal(err)
	}
	la := s.LocalAuthority()
	a, err := la.CreateAuthority(ctx, "cred-1", host.ID, "reviewed the second revision")
	if err != nil {
		t.Fatal(err)
	}
	if err = la.GrantTool(ctx, "cred-1", a.ID, "dev_read", "r"); err != nil {
		t.Fatal(err)
	}
	if names, _ := la.ResolveToolNames(ctx, host.ID, updated.Revision); strings.Join(names, ",") != "dev_read" {
		t.Fatalf("view admitted under the reviewed revision: %v", names)
	}
	if names, _ := la.ResolveToolNames(ctx, host.ID, older); len(names) != 0 {
		t.Fatalf("a view admitted under an older revision inherited a later review: %v", names)
	}
	for _, blank := range [][2]string{{"", updated.Revision}, {host.ID, ""}} {
		if names, err := la.ResolveToolNames(ctx, blank[0], blank[1]); err != nil || len(names) != 0 {
			t.Fatalf("blank input %v resolved %v, %v", blank, names, err)
		}
	}
}
