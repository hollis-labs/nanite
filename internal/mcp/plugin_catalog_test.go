package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

// This fixture supplies a trusted host binding; it is not public enrollment.
type catalogCallerFixture struct {
	manifestToolFixture
	owner  capability.RuntimeIdentity
	digest string
}

func (f *catalogCallerFixture) Incarnation() capability.RuntimeIdentity { return f.owner }
func (f *catalogCallerFixture) AcceptedReviewDigest() string            { return f.digest }

type catalogPolicyFixture struct {
	denied       error
	visible      bool
	verified     int
	onVisible    func(AcceptedPluginTool)
	onAuthorize  func(AcceptedPluginTool, map[string]any, string)
	onRevalidate func() error
}

func (f *catalogPolicyFixture) VerifyCaller(context.Context) error { f.verified++; return f.denied }
func (f *catalogPolicyFixture) Visible(_ context.Context, tool AcceptedPluginTool) (bool, error) {
	if f.onVisible != nil {
		f.onVisible(tool)
	}
	return f.visible, nil
}
func (f *catalogPolicyFixture) AuthorizeCall(_ context.Context, tool AcceptedPluginTool, args map[string]any, key string) error {
	if f.onAuthorize != nil {
		f.onAuthorize(tool, args, key)
	}
	return nil
}
func (f *catalogPolicyFixture) RevalidateCall(context.Context, AcceptedPluginTool, map[string]any, string) error {
	if f.onRevalidate != nil {
		return f.onRevalidate()
	}
	return nil
}
func newCatalogFixture(t *testing.T) (*Manager, *catalogCallerFixture, *manifestPluginTransport, *catalogPolicyFixture, *PluginCatalog) {
	t.Helper()
	m := NewManager()
	caller := &catalogCallerFixture{owner: capability.RuntimeIdentity{HostInstance: "host", OwnerID: "plugin", OwnerGeneration: 1}, digest: "accepted-definition"}
	caller.result = sdkprocess.MCPCallResult{Content: []byte(`"business error"`), IsError: true}
	declaration := declaredFixtureTool("reviewed", pluginapi.ToolEffectWrite)
	declaration.Annotations = &manifest.ToolAnnotations{}
	transport, err := newManifestPluginTransport(caller, []manifest.Tool{declaration}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := &toolEntry{serverName: "plugin", uniformName: "reviewed", tool: transport.declarations[0]}
	m.servers["plugin"] = transport
	m.tools = append(m.tools, entry)
	m.uniformIndex[entry.uniformName] = entry
	policy := &catalogPolicyFixture{visible: true}
	return m, caller, transport, policy, NewPluginCatalog(m, policy)
}

func TestPluginCatalogRequiresVerifiedPolicyBeforeInventory(t *testing.T) {
	ctx := context.Background()
	if _, err := NewPluginCatalog(nil, nil).List(ctx); !errors.Is(err, ErrPluginCatalogUnavailable) {
		t.Fatal(err)
	}
	denied := errors.New("unverified host binding")
	policy := &catalogPolicyFixture{denied: denied}
	// Nil manager maps would panic if inventory access preceded VerifyCaller.
	c := NewPluginCatalog(&Manager{}, policy)
	if _, err := c.List(ctx); !errors.Is(err, denied) || policy.verified != 1 {
		t.Fatal("inventory read before caller verification", err)
	}
	if _, err := c.Call(ctx, "x", "x", "x", nil, ""); !errors.Is(err, denied) {
		t.Fatal(err)
	}
}

func TestPluginCatalogUsesOnlyAcceptedDetachedMetadata(t *testing.T) {
	m, caller, _, policy, c := newCatalogFixture(t)
	// A global/core entry and discovery metadata must never supply declarations.
	m.tools = append(m.tools, &toolEntry{serverName: "core", uniformName: "core-only", tool: Tool{Name: "core-only"}})
	m.tools[0].tool = Tool{Name: "reviewed", Description: "mutable UI description", InputSchema: map[string]any{"type": "array"}}
	policy.onVisible = func(tool AcceptedPluginTool) {
		if tool.Tool.Description != "Reviewed tool" || tool.Effect != pluginapi.ToolEffectWrite || tool.Tool.Annotations != nil || tool.Owner != caller.owner || tool.ReviewDigest != caller.digest {
			t.Fatal("not exact accepted metadata", tool)
		}
		tool.Tool.InputSchema["type"] = "array"
	}
	catalog, err := c.List(context.Background())
	if err != nil || len(catalog.Tools) != 1 {
		t.Fatal(catalog, err)
	}
	entry := catalog.Tools[0]
	if entry.Tool.InputSchema["type"] != "object" {
		t.Fatal("policy mutated returned schema")
	}
	entry.Tool.InputSchema["type"] = "array"
	next, err := c.List(context.Background())
	if err != nil || next.Tools[0].Tool.InputSchema["type"] != "object" || next.Tools[0].Binding != entry.Binding {
		t.Fatal("caller mutated accepted metadata", err)
	}
	policy.visible = false
	empty, err := c.List(context.Background())
	if err != nil || empty.Tools == nil || len(empty.Tools) != 0 || empty.Revision == "" {
		t.Fatal("authoritative empty confused with unavailable", empty, err)
	}
}

func TestPluginCatalogCallPinsAcceptanceAndPreservesBusinessResult(t *testing.T) {
	_, caller, _, policy, c := newCatalogFixture(t)
	catalog, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tool := catalog.Tools[0]
	policy.onAuthorize = func(entry AcceptedPluginTool, args map[string]any, key string) {
		if key != "host-receipt-key" || entry.Binding != tool.Binding {
			t.Fatal("lost owner inputs")
		}
		args["value"] = "changed by policy"
		entry.Tool.InputSchema["type"] = "array"
	}
	result, err := c.Call(context.Background(), catalog.Revision, tool.Tool.Name, tool.Binding, map[string]any{"value": "original"}, "host-receipt-key")
	if err != nil || !result.IsError || caller.calls != 1 || caller.request.Arguments["value"] != "original" {
		t.Fatal("call metadata or business outcome changed", result, err)
	}
}

func TestPluginCatalogRefusesDriftAndPolicyWithdrawalBeforeBytes(t *testing.T) {
	for _, kind := range []string{"generation", "review", "schema", "annotations", "removal", "replacement", "policy"} {
		t.Run(kind, func(t *testing.T) {
			m, caller, transport, policy, c := newCatalogFixture(t)
			catalog, err := c.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			tool := catalog.Tools[0]
			policy.onAuthorize = func(AcceptedPluginTool, map[string]any, string) {
				// Callback can take the manager lock: catalog holds none across policy.
				m.mu.Lock()
				defer m.mu.Unlock()
				switch kind {
				case "generation":
					caller.owner.OwnerGeneration++
				case "review":
					caller.digest = "other acceptance"
				case "schema":
					transport.declarations[0].InputSchema["type"] = "array"
				case "annotations":
					transport.declarations[0].Annotations = map[string]any{"readOnlyHint": false}
				case "removal":
					delete(m.servers, "plugin")
				case "replacement":
					replacement := *transport
					m.servers["plugin"] = &replacement
				case "policy":
					policy.denied = ErrPluginCatalogForbidden
				}
			}
			_, err = c.Call(context.Background(), catalog.Revision, tool.Tool.Name, tool.Binding, nil, "owner-key")
			if kind == "policy" {
				if !errors.Is(err, ErrPluginCatalogForbidden) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, subprocess.ErrStaleBinding) {
				t.Fatal(err)
			}
			if caller.calls != 0 {
				t.Fatal("drift reached connection")
			}
		})
	}
}
