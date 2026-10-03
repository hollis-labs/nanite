package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

func alwaysShipReviewBundle(t *testing.T) (string, string) {
	t.Helper()
	common, err := manifest.Decode(strings.NewReader(sharedManifestBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	common.Config = manifest.Config{}
	common.Nanite = json.RawMessage(`{"registers":{"always_ship_sources":[{"id":"pins","title":"Pinned Context","list_tool":"pins_list"}]}}`)
	common.Tools = []manifest.Tool{{Name: "pins_list", Effect: "read", Description: "Inventory", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	common.Capabilities = []sdkprocess.CapabilityRequest{{Name: "context.always_ship", Metadata: json.RawMessage(`{"source_ids":["pins"],"all_sessions":true,"max_bytes":6000}`)}}
	var out strings.Builder
	if checkErr := manifest.Encode(&out, common); checkErr != nil {
		t.Fatal(checkErr)
	}
	root, directory := reviewBundle(t)
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(out.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.MkdirAll(filepath.Join(directory, "bin"), 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "bin/plugin"), []byte("#!/bin/sh\nexit 1\n"), 0700); checkErr != nil { // #nosec G306 -- isolated executable test fixture requires owner execute permission.
		t.Fatal(checkErr)
	}
	return root, directory
}
func TestAlwaysShipInstallReviewAndApproval(t *testing.T) {
	root, dir := alwaysShipReviewBundle(t)
	ctx := context.Background()
	review, err := BuildInstallReview(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"system prompt", "1500 uncached tokens per turn", "competes with your own context", "exempt from intent skipping", "example.plugin", "Example", "pins_list", "declared read effect"} {
		if !strings.Contains(review.HostNotice, fragment) {
			t.Fatal("weak review warning", fragment, review.HostNotice)
		}
	}
	encoded, err := json.Marshal(review)
	if err != nil || !strings.Contains(string(encoded), `"host_notice":`) {
		t.Fatal("warning absent from object", err)
	}
	if _, launchErr := ResolveReviewedLaunch(ctx, dir); launchErr == nil {
		t.Fatal("unreviewed launch accepted")
	}
	if checkErr := SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	launch, err := ResolveReviewedLaunch(ctx, dir)
	if err != nil || !strings.Contains(strings.Join(launch.Granted, ","), "context.always_ship") {
		t.Fatal("required accepted capability not granted", launch, err)
	}
	changed := review
	changed.HostNotice = "weaker"
	if changed.Digest() == review.Digest() {
		t.Fatal("notice not digested")
	}
	path, err := ApprovalPath(root, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := os.Remove(path); checkErr != nil {
		t.Fatal(checkErr)
	}
	if err := CheckAcceptedBundle(ctx, dir, launch.ReviewDigest); err == nil {
		t.Fatal("revoked approval survived")
	}
}
func TestAlwaysShipAbsentNoticePreservesExistingReviewDigest(t *testing.T) {
	_, dir := reviewBundle(t)
	review, err := BuildInstallReview(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	if review.HostNotice != "" || strings.Contains(string(encoded), "host_notice") {
		t.Fatal("ordinary review gained notice")
	}
	// Decode/re-encode through the pre-existing wire object: omitting the new
	// member must preserve the exact bytes that older approvals digested.
	var old struct {
		ID           string                         `json:"id"`
		Name         string                         `json:"name"`
		Version      string                         `json:"version"`
		BundleDigest string                         `json:"bundle_digest"`
		Arguments    []string                       `json:"arguments"`
		Entrypoint   string                         `json:"entrypoint"`
		Capabilities []sdkprocess.CapabilityRequest `json:"capabilities"`
		Secrets      []ReviewSecret                 `json:"secrets"`
		Environment  []string                       `json:"environment"`
		Tools        []ReviewTool                   `json:"tools"`
		ToolLoadType LoadType                       `json:"tool_load_type,omitempty"`
		ReflexSeeds  []pluginapi.ReflexSeed         `json:"reflex_seeds,omitempty"`
	}
	if checkErr := json.Unmarshal(encoded, &old); checkErr != nil {
		t.Fatal(checkErr)
	}
	original, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	if string(original) != string(encoded) || hex.EncodeToString(sum[:]) != review.Digest() {
		t.Fatal("older approval changed")
	}
}
func TestAlwaysShipInstallRejectsInvalidAuthorityAndCoreTitles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*manifest.Manifest)
	}{
		{"missing grant", func(m *manifest.Manifest) { m.Capabilities = nil }},
		{"optional grant", func(m *manifest.Manifest) { m.Capabilities[0].Optional = true }},
		{"write tool", func(m *manifest.Manifest) { m.Tools[0].Effect = "write" }},
		{"requires arguments", func(m *manifest.Manifest) {
			m.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
		}},
		{"core documents", func(m *manifest.Manifest) {
			m.Nanite = json.RawMessage(`{"registers":{"always_ship_sources":[{"id":"docs","title":"session._- Documents","list_tool":"pins_list"}]}}`)
			m.Capabilities[0].Metadata = json.RawMessage(`{"source_ids":["docs"],"all_sessions":true,"max_bytes":6000}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, dir := alwaysShipReviewBundle(t)
			raw, err := os.ReadFile(filepath.Join(dir, "plugin.yaml")) // #nosec G304 -- fixed filename within this test's temporary bundle.
			if err != nil {
				t.Fatal(err)
			}
			common, err := manifest.Decode(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&common)
			raw, err = json.Marshal(common)
			if err != nil {
				t.Fatal(err)
			}
			if checkErr := os.WriteFile(filepath.Join(dir, "plugin.yaml"), raw, 0600); checkErr != nil { // #nosec G703 -- only this test's generated temporary bundle is rewritten.
				t.Fatal(checkErr)
			}
			if _, err := BuildInstallReview(context.Background(), dir); err == nil {
				t.Fatal("invalid authority reached review")
			}
		})
	}
}

type alwaysShipRegistrarProbe struct {
	mu      sync.Mutex
	removed []string
	added   int
}

func (p *alwaysShipRegistrarProbe) AddPluginAlwaysShipSources(string, []pluginapi.AlwaysShipSource, pluginapi.AlwaysShipScope, *subprocess.SubprocessPlugin, func(context.Context) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.added++
	return nil
}
func (p *alwaysShipRegistrarProbe) RemovePluginAlwaysShipSources(owner string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removed = append(p.removed, owner)
}
func (p *alwaysShipRegistrarProbe) removedOwners() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.removed...)
}
func (p *alwaysShipRegistrarProbe) addedCount() int { p.mu.Lock(); defer p.mu.Unlock(); return p.added }
func TestAlwaysShipDisposedByHostAndManagerUnload(t *testing.T) {
	for _, mode := range []string{"host", "manager", "shutdown", "failed-load"} {
		t.Run(mode, func(t *testing.T) {
			host := NewHost(http.NewServeMux(), NewLogger("always-ship-test"))
			reg := &alwaysShipRegistrarProbe{}
			host.SetAlwaysShipRegistrar(reg)
			if mode == "host" || mode == "shutdown" {
				if checkErr := host.LoadPlugin(&fakePlugin{id: "example.plugin"}); checkErr != nil {
					t.Fatal(checkErr)
				}
				var err error
				if mode == "host" {
					err = host.UnloadPlugin("example.plugin")
				} else {
					err = host.Shutdown()
				}
				if err != nil {
					t.Fatal(err)
				}
			} else {
				root, dir := alwaysShipReviewBundle(t)
				review, err := BuildInstallReview(context.Background(), dir)
				if err != nil {
					t.Fatal(err)
				}
				if checkErr := SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
					t.Fatal(checkErr)
				}
				m, err := ParseManifest(filepath.Join(dir, "plugin.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				child, err := NewSubprocessPluginFromManifest(context.Background(), DiscoveredPlugin{Dir: dir, Manifest: m}, host)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "manager" {
					if checkErr := child.Unload(); checkErr != nil {
						t.Fatal(checkErr)
					}
				} else {
					// This deliberately failing shell fixture cannot call a model or tools.
					if err := child.Load(host); err == nil {
						t.Fatal("failing fixture loaded")
					}
				}
			}
			if len(reg.removedOwners()) == 0 || reg.removedOwners()[0] != "example.plugin" {
				t.Fatal("lease not disposed", mode, reg.removedOwners())
			}
		})
	}
}

func TestAlwaysShipDisabledPluginDoesNotAcquireLease(t *testing.T) {
	root, dir := alwaysShipReviewBundle(t)
	review, err := BuildInstallReview(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	host := NewHost(http.NewServeMux(), NewLogger("disabled-always-ship"))
	reg := &alwaysShipRegistrarProbe{}
	host.SetAlwaysShipRegistrar(reg)
	host.SetMCPRegistrar(&alwaysShipToolRegistrarProbe{newStubMCPRegistrar()})
	db := newTestPluginStateStore(t)
	host.SetStore(db)
	if checkErr := db.SetPluginEnabled(context.Background(), review.ID, "subprocess", false); checkErr != nil {
		t.Fatal(checkErr)
	}
	m, err := ParseManifest(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewSubprocessPluginFromManifest(context.Background(), DiscoveredPlugin{Dir: dir, Manifest: m}, host)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := applyManifestRegistrations(host, m, child, dir); checkErr != nil {
		t.Fatal(checkErr)
	}
	if reg.addedCount() != 0 {
		t.Fatal("disabled plugin acquired lease")
	}
	if checkErr := db.SetPluginEnabled(context.Background(), review.ID, "subprocess", true); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := applyManifestRegistrations(host, m, child, dir); checkErr != nil {
		t.Fatal(checkErr)
	}
	if reg.addedCount() != 1 {
		t.Fatal("enabled approved source not registered")
	}
	if checkErr := child.Unload(); checkErr != nil {
		t.Fatal(checkErr)
	}
	if len(reg.removedOwners()) == 0 {
		t.Fatal("disable's child unload did not dispose lease")
	}
}

type alwaysShipToolRegistrarProbe struct{ *stubMCPRegistrar }

func (*alwaysShipToolRegistrarProbe) AddPluginTools(string, []manifest.Tool, string, *subprocess.SubprocessPlugin, subprocess.EnvelopeConsumer) error {
	return nil
}

func TestAlwaysShipReapprovalCannotAuthorizeOlderChild(t *testing.T) {
	root, dir := alwaysShipReviewBundle(t)
	ctx := context.Background()
	review, err := BuildInstallReview(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	oldManifest, err := ParseManifest(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	host := NewHost(http.NewServeMux(), NewLogger("stale-always-ship"))
	reg := &alwaysShipRegistrarProbe{}
	host.SetAlwaysShipRegistrar(reg)
	host.SetMCPRegistrar(&alwaysShipToolRegistrarProbe{newStubMCPRegistrar()})
	child, err := NewSubprocessPluginFromManifest(ctx, DiscoveredPlugin{Dir: dir, Manifest: oldManifest}, host)
	if err != nil {
		t.Fatal(err)
	}
	if child.AcceptedReviewDigest() != review.Digest() {
		t.Fatal("manager did not pin its original review")
	}
	// New reviewed bytes narrow session authority while the old manifest/child
	// snapshot still declares all_sessions. The old lease must never use the
	// replacement receipt, even if the replacement is fully operator-approved.
	common := *oldManifest.Shared
	common.Capabilities = append([]sdkprocess.CapabilityRequest(nil), common.Capabilities...)
	common.Capabilities[0].Metadata = json.RawMessage(`{"source_ids":["pins"],"session_ids":["narrow"],"max_bytes":6000}`)
	raw, err := json.Marshal(common)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := os.WriteFile(filepath.Join(dir, "plugin.yaml"), raw, 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	updated, err := BuildInstallReview(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := SaveInstallApproval(root, updated, updated.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := applyManifestRegistrations(host, oldManifest, child, dir); checkErr == nil {
		t.Fatal("new receipt authorized old all-session declaration")
	}
	if reg.addedCount() != 0 {
		t.Fatal("stale declaration acquired lease")
	}
	if checkErr := child.Unload(); checkErr != nil {
		t.Fatal(checkErr)
	}
}
