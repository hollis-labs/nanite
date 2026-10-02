package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestHostQueryGrantLifecycle(t *testing.T) {
	h := NewHost(nil, NewLogger("query-test"))
	if checkErr := h.SetHostQueryURL("http://127.0.0.1:8090"); checkErr != nil {
		t.Fatal(checkErr)
	}
	scope := pluginapi.QueryScope{Resources: []pluginapi.QueryResource{pluginapi.QuerySessions}, SessionIDs: []string{"allowed"}}
	grant, err := h.prepareHostQueryGrant("reader", scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.AuthorizeHostQuery(grant.Token); ok {
		t.Fatal("unbound token accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if checkErr := h.bindHostQueryGrant(canceled, grant); checkErr == nil {
		t.Fatal("canceled spawn bound token")
	}
	if checkErr := h.bindHostQueryGrant(context.Background(), grant); checkErr != nil {
		t.Fatal(checkErr)
	}
	permit, ok := h.AuthorizeHostQuery(grant.Token)
	if !ok || permit.PluginID != "reader" || !permit.Scope.Allows(pluginapi.QuerySessions, "allowed") {
		t.Fatal("grant missing")
	}
	permit.Scope.SessionIDs[0] = "other"
	grant.Scope.SessionIDs[0] = "other"
	// The same connection may restart, but cannot expand its original scope.
	if checkErr := h.bindHostQueryGrant(context.Background(), grant); checkErr != nil {
		t.Fatal(checkErr)
	}
	original, ok := h.AuthorizeHostQuery(grant.Token)
	if !ok || original.Scope.Allows(pluginapi.QuerySessions, "other") {
		t.Fatal("mutable scope escaped")
	}
	grant.PluginID = "intruder"
	if checkErr := h.bindHostQueryGrant(context.Background(), grant); checkErr == nil {
		t.Fatal("cross-owner token accepted")
	}
	duplicate, err := h.prepareHostQueryGrant("reader", scope)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := h.bindHostQueryGrant(context.Background(), duplicate); checkErr == nil {
		t.Fatal("second connection replaced owner")
	}
	if checkErr := h.SetHostQueryURL("http://127.0.0.1:8091"); checkErr == nil {
		t.Fatal("origin changed under active grant")
	}
	var readers sync.WaitGroup
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 100 {
				h.AuthorizeHostQuery(grant.Token)
			}
		}()
	}
	h.revokeHostQueryGrant(grant.Token)
	readers.Wait()
	if original.Context.Err() == nil {
		t.Fatal("revocation did not cancel reads")
	}
	if _, ok := h.AuthorizeHostQuery(grant.Token); ok {
		t.Fatal("revoked token accepted")
	}
	if _, ok := h.AuthorizeHostQuery("invalid"); ok {
		t.Fatal("invalid token accepted")
	}
	if checkErr := h.bindHostQueryGrant(context.Background(), duplicate); checkErr != nil {
		t.Fatal(checkErr)
	}
	h.ctxCancel()
	if _, ok := h.AuthorizeHostQuery(duplicate.Token); ok {
		t.Fatal("stopped host authorized reads")
	}
}

func TestHostQueryFailedLaunchRevokesGrant(t *testing.T) {
	root, directory := reviewBundle(t)
	raw := sharedManifestBytes(t)
	declaration, err := manifest.Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	declaration.Config = manifest.Config{}
	declaration.Entrypoint = manifest.Entrypoint{Command: "plugin"}
	declaration.Capabilities = []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityReadOnlyQuery, Metadata: json.RawMessage(`{"resources":["sessions"],"all_sessions":true}`)}}
	binary, err := os.ReadFile("/bin/false")
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G306 G703 -- executable fixture copied into a private test bundle; 0700 is required to launch it.
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin"), binary, 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	approve := func() *PluginManifest {
		t.Helper()
		var encoded strings.Builder
		if checkErr := manifest.Encode(&encoded, declaration); checkErr != nil {
			t.Fatal(checkErr)
		}
		path := filepath.Join(directory, "plugin.yaml")
		if checkErr := os.WriteFile(path, []byte(encoded.String()), 0600); checkErr != nil {
			t.Fatal(checkErr)
		}
		review, reviewErr := BuildInstallReview(context.Background(), directory)
		if reviewErr != nil {
			t.Fatal(reviewErr)
		}
		if checkErr := SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
			t.Fatal(checkErr)
		}
		parsed, reviewErr := ParseManifest(path)
		if reviewErr != nil {
			t.Fatal(reviewErr)
		}
		return parsed
	}
	parsed := approve()
	host := NewHost(nil, NewLogger("failed-query"))
	dp := DiscoveredPlugin{Dir: directory, Manifest: parsed}
	if _, checkErr := NewSubprocessPluginFromManifest(context.Background(), dp, host); checkErr == nil {
		t.Fatal("required query launched without host origin")
	}
	declaration.Capabilities[0].Optional = true
	dp.Manifest = approve()
	if _, checkErr := NewSubprocessPluginFromManifest(context.Background(), dp, host); checkErr != nil {
		t.Fatalf("optional query refusal prevented construction: %v", checkErr)
	}
	if checkErr := host.SetHostQueryURL("http://127.0.0.1:8090"); checkErr != nil {
		t.Fatal(checkErr)
	}
	child, err := NewSubprocessPluginFromManifest(context.Background(), dp, host)
	if err != nil {
		t.Fatal(err)
	}
	if len(host.queryGrants) != 0 {
		t.Fatal("construction bound credential")
	}
	if checkErr := host.LoadPlugin(child); checkErr == nil {
		t.Fatal("failed subprocess loaded")
	}
	if len(host.queryGrants) != 0 {
		t.Fatal("failed handshake retained credential")
	}
	declaration.Capabilities[0].Metadata = json.RawMessage(`{"resources":["sessions"],"all_sessions":true,"sql":"SELECT *"}`)
	var invalid strings.Builder
	if checkErr := manifest.Encode(&invalid, declaration); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(invalid.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := BuildInstallReview(context.Background(), directory); checkErr == nil {
		t.Fatal("unrecognized query scope accepted for review")
	}
}
