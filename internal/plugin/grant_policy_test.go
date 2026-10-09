package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPluginGrantPolicyExplicitDefaultAndConfiguration(t *testing.T) {
	t.Setenv(PluginGrantLifetimeEnv, "")
	ttl, revision, err := pluginGrantPolicy("accepted-bytes")
	if err != nil || ttl != 24*time.Hour || revision == "" {
		t.Fatalf("policy: %v %v", ttl, err)
	}
	t.Setenv(PluginGrantLifetimeEnv, "2h")
	ttl, next, err := pluginGrantPolicy("accepted-bytes")
	if err != nil || ttl != 2*time.Hour || next == revision {
		t.Fatal("configured policy ignored")
	}
	for _, value := range []string{"0", "-1h", "forever"} {
		t.Setenv(PluginGrantLifetimeEnv, value)
		if _, _, err := pluginGrantPolicy("accepted-bytes"); err == nil {
			t.Fatal("invalid lifetime accepted")
		}
	}
}

func TestHostCredentialsExpireWithIncarnationLease(t *testing.T) {
	h := NewHost(nil, NewLogger("lease-test"))
	if err := h.SetHostQueryURL("http://127.0.0.1:8090"); err != nil {
		t.Fatal(err)
	}
	query, err := h.prepareHostQueryGrant("reader", pluginapi.QueryScope{Resources: []pluginapi.QueryResource{pluginapi.QuerySessions}, SessionIDs: []string{"one"}})
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := h.bindHostQueryGrant(context.Background(), query); checkErr != nil {
		t.Fatal(checkErr)
	}
	// No capability grants means no capability privilege; lifecycle revocation
	// still ends this owner credential and all permits.
	owner, err := subprocess.NewOwnerIncarnation(context.Background(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := subprocess.NewGrantLease(context.Background(), nil, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.attachHostQueryLease(query.Token, lease, nil)
	permit, ok := h.AuthorizeHostQuery(query.Token)
	if !ok {
		t.Fatal("bound credential refused")
	}
	lease.Revoke()
	if permit.Context.Err() == nil {
		t.Fatal("in-flight bearer permit survived")
	}
	if _, ok := h.AuthorizeHostQuery(query.Token); ok {
		t.Fatal("expired/revoked credential accepted")
	}
	if _, _, err := lease.Acquire(context.Background()); !errors.Is(err, subprocess.ErrGrantLeaseEnded) {
		t.Fatal(err)
	}
}

func TestReviewedGrantsPreserveNamespacedScopeAndFreshOwner(t *testing.T) {
	root, directory := reviewBundle(t)
	declaration, err := manifest.Decode(strings.NewReader(sharedManifestBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	declaration.Config = manifest.Config{}
	scope := json.RawMessage(`{"resources":["sessions","context_slots"],"all_sessions":true,"include_content":true}`)
	declaration.Capabilities = []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityReadOnlyQuery, Metadata: scope}}
	var encoded strings.Builder
	if checkErr := manifest.Encode(&encoded, declaration); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(encoded.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	review, err := BuildInstallReview(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	launch, err := ResolveReviewedLaunch(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := subprocess.NewOwnerIncarnation(t.Context(), review.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Caller-side metadata cannot replace the accepted scope.
	launch.Scopes[pluginapi.CapabilityReadOnlyQuery] = json.RawMessage(`{"resources":["usage"],"all_sessions":true}`)
	grants, err := issueReviewedGrants(t.Context(), directory, launch, owner, launch.Granted)
	if err != nil || len(grants) != 1 {
		t.Fatalf("issued: %v %v", grants, err)
	}
	grant := grants[0]
	if grant.Name != "host.nanite.readonly.query" || grant.SchemaVersion != NaniteGrantSchemaVersion || grant.Audience != "nanite" {
		t.Fatalf("descriptor: %+v", grant)
	}
	got, err := pluginapi.DecodeQueryScope(grant.Scope)
	want, _ := pluginapi.DecodeQueryScope(scope)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("scope reinterpreted: %+v %v", got, err)
	}
	issued, _ := time.Parse(time.RFC3339Nano, grant.IssuedAt)
	expires, _ := time.Parse(time.RFC3339Nano, grant.ExpiresAt)
	if expires.Sub(issued) != DefaultPluginGrantLifetime {
		t.Fatal("default lifetime lost")
	}
	if err := grants.ValidateForRuntime(owner); err != nil {
		t.Fatal(err)
	}
	wrong := owner
	wrong.OwnerID = "different"
	if _, err := issueReviewedGrants(t.Context(), directory, launch, wrong, launch.Granted); err == nil {
		t.Fatal("cross-owner issuance accepted")
	}
	if _, err := issueReviewedGrants(t.Context(), directory, launch, owner, []string{"docker_socket"}); err == nil {
		t.Fatal("undeclared capability issued")
	}
	if err := os.WriteFile(filepath.Join(directory, "asset.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := revalidateReviewedGrants(t.Context(), directory, launch.ReviewDigest, grants); err == nil {
		t.Fatal("changed accepted bytes retained authority")
	}
}

func TestNaniteGrantDescriptorsRefuseUnknownOrExpandedScope(t *testing.T) {
	for _, test := range []struct{ name, raw string }{
		{pluginapi.CapabilityReadOnlyQuery, `{"resources":["sessions"],"all_sessions":true,"sql":"all"}`},
		{pluginapi.CapabilityContextSource, `{"source_ids":["one"],"all_sessions":true,"include_query":true,"mounts":["*"]}`},
		{pluginapi.CapabilityContextAlwaysShip, `{"source_ids":["one"],"all_sessions":true,"max_bytes":0}`},
		{pluginapi.CapabilityDurableWake, `{"agent_slugs":["*"]}`},
		{"ssh_agent", `{"environment":["OTHER"]}`},
		{"unknown", `{}`},
	} {
		if _, err := reviewedGrantScope(test.name, json.RawMessage(test.raw)); err == nil {
			t.Fatalf("expanded descriptor accepted %s", test.name)
		}
	}
	if _, err := reviewedGrantScope("ssh_agent", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	// Portable raw scopes retain dynamic scope as a boolean, never a wildcard ID.
	grant := capability.Grant{GrantID: "scope", Name: "host.nanite.context.source", SchemaVersion: NaniteGrantSchemaVersion, Scope: json.RawMessage(`{"source_ids":["one"],"all_sessions":true,"include_query":true}`), HostInstance: "host", OwnerID: "owner", OwnerGeneration: 1, Audience: "nanite", IssuedAt: "2026-10-09T01:00:00Z", ExpiresAt: "2026-10-10T01:00:00Z", PolicyRevision: "test"}
	if err := grant.Validate(); err != nil {
		t.Fatal(err)
	}
}
