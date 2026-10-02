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

func TestHostDurableWakeGrantLifecycle(t *testing.T) {
	h := NewHost(nil, NewLogger("wake-test"))
	if checkErr := h.SetHostQueryURL("http://127.0.0.1:8090"); checkErr != nil {
		t.Fatal(checkErr)
	}
	scope := pluginapi.DurableWakeScope{AgentSlugs: []string{"allowed"}}
	grant, err := h.prepareHostDurableWakeGrant("reader", scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.AuthorizeHostDurableWake(grant.Token); ok {
		t.Fatal("unbound token accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if checkErr := h.bindHostDurableWakeGrant(canceled, grant); checkErr == nil {
		t.Fatal("canceled spawn bound token")
	}
	if checkErr := h.bindHostDurableWakeGrant(context.Background(), grant); checkErr != nil {
		t.Fatal(checkErr)
	}
	permit, ok := h.AuthorizeHostDurableWake(grant.Token)
	if !ok || permit.PluginID != "reader" || !permit.Scope.Allows("allowed") {
		t.Fatal("grant missing")
	}
	permit.Scope.AgentSlugs[0] = "other"
	grant.Scope.AgentSlugs[0] = "other"
	// The same connection may restart, but cannot expand its original scope.
	if checkErr := h.bindHostDurableWakeGrant(context.Background(), grant); checkErr != nil {
		t.Fatal(checkErr)
	}
	original, ok := h.AuthorizeHostDurableWake(grant.Token)
	if !ok || original.Scope.Allows("other") {
		t.Fatal("mutable scope escaped")
	}
	grant.PluginID = "intruder"
	if checkErr := h.bindHostDurableWakeGrant(context.Background(), grant); checkErr == nil {
		t.Fatal("cross-owner token accepted")
	}
	duplicate, err := h.prepareHostDurableWakeGrant("reader", scope)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := h.bindHostDurableWakeGrant(context.Background(), duplicate); checkErr == nil {
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
				h.AuthorizeHostDurableWake(grant.Token)
			}
		}()
	}
	h.revokeHostDurableWakeGrant(grant.Token)
	readers.Wait()
	if original.Context.Err() == nil {
		t.Fatal("revocation did not cancel wakes")
	}
	if _, ok := h.AuthorizeHostDurableWake(grant.Token); ok {
		t.Fatal("revoked token accepted")
	}
	if _, ok := h.AuthorizeHostDurableWake("invalid"); ok {
		t.Fatal("invalid token accepted")
	}
	if checkErr := h.bindHostDurableWakeGrant(context.Background(), duplicate); checkErr != nil {
		t.Fatal(checkErr)
	}
	h.ctxCancel()
	if _, ok := h.AuthorizeHostDurableWake(duplicate.Token); ok {
		t.Fatal("stopped host authorized wakes")
	}
}

func TestHostDurableWakeFailedLaunchRevokesGrant(t *testing.T) {
	root, directory := reviewBundle(t)
	raw := sharedManifestBytes(t)
	declaration, err := manifest.Decode(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	declaration.Config = manifest.Config{}
	declaration.Entrypoint = manifest.Entrypoint{Command: "plugin"}
	declaration.Capabilities = []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityDurableWake, Metadata: json.RawMessage(`{"agent_slugs":["loom-curator"]}`)}}
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
	host := NewHost(nil, NewLogger("failed-wake"))
	dp := DiscoveredPlugin{Dir: directory, Manifest: parsed}
	if _, checkErr := NewSubprocessPluginFromManifest(context.Background(), dp, host); checkErr == nil {
		t.Fatal("required wake launched without host origin")
	}
	declaration.Capabilities[0].Optional = true
	dp.Manifest = approve()
	if _, checkErr := NewSubprocessPluginFromManifest(context.Background(), dp, host); checkErr != nil {
		t.Fatalf("optional wake refusal prevented construction: %v", checkErr)
	}
	if checkErr := host.SetHostQueryURL("http://127.0.0.1:8090"); checkErr != nil {
		t.Fatal(checkErr)
	}
	child, err := NewSubprocessPluginFromManifest(context.Background(), dp, host)
	if err != nil {
		t.Fatal(err)
	}
	if len(host.wakeGrants) != 0 {
		t.Fatal("construction bound credential")
	}
	if checkErr := host.LoadPlugin(child); checkErr == nil {
		t.Fatal("failed subprocess loaded")
	}
	if len(host.wakeGrants) != 0 {
		t.Fatal("failed handshake retained credential")
	}
	declaration.Capabilities[0].Metadata = json.RawMessage(`{"agent_slugs":["loom-curator"],"sql":"SELECT *"}`)
	var invalid strings.Builder
	if checkErr := manifest.Encode(&invalid, declaration); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(invalid.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := BuildInstallReview(context.Background(), directory); checkErr == nil {
		t.Fatal("unrecognized wake scope accepted for review")
	}
}
