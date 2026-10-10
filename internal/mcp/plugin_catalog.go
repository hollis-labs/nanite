package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

var ErrPluginCatalogUnavailable = errors.New("verified plugin catalog policy unavailable")
var ErrPluginCatalogForbidden = errors.New("plugin tool unavailable to caller")

// PluginCatalogPolicy is a trusted execution-owner port, not a caller's claimed
// identity. Implementations must verify their host binding and current caller
// policy, and own schema, effect, approval, operation receipt and commit rules.
// RevalidateCall must only check existing authority; it must not create another
// approval or receipt. No policy implementation or public registration is supplied
// by this internal candidate.
type PluginCatalogPolicy interface {
	VerifyCaller(context.Context) error
	Visible(context.Context, AcceptedPluginTool) (bool, error)
	AuthorizeCall(context.Context, AcceptedPluginTool, map[string]any, string) error
	RevalidateCall(context.Context, AcceptedPluginTool, map[string]any, string) error
}

// AcceptedPluginTool is a detached snapshot of one accepted manifest declaration.
// Binding correlates this metadata and inventory with a live owner; it is not an
// authentication credential, approval, or grant.
type AcceptedPluginTool struct {
	Tool         Tool
	Effect       string
	Owner        capability.RuntimeIdentity
	ReviewDigest string
	Binding      string
}

type AcceptedPluginCatalog struct {
	Revision string
	Tools    []AcceptedPluginTool
}

type PluginCatalog struct {
	manager *Manager
	policy  PluginCatalogPolicy
}

func NewPluginCatalog(manager *Manager, policy PluginCatalogPolicy) *PluginCatalog {
	return &PluginCatalog{manager: manager, policy: policy}
}

type acceptedPluginOwner interface {
	Incarnation() capability.RuntimeIdentity
	AcceptedReviewDigest() string
}

type catalogSelection struct {
	entry     AcceptedPluginTool
	transport *manifestPluginTransport
	name      string
}

func cloneCatalogValue[T any](value T) (T, error) {
	var out T
	raw, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}

func catalogHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// snapshot reads only manifest-owned declarations. It never consults core,
// profiles, plugin-supplied discovery or a substitute owner after a restart.
func (c *PluginCatalog) snapshot() (string, []catalogSelection, error) {
	m := c.manager
	m.mu.RLock()
	selections := make([]catalogSelection, 0)
	checker := m.LoadChecker
	for _, item := range m.tools {
		transport, ok := m.servers[item.serverName].(*manifestPluginTransport)
		if !ok {
			continue
		}
		owner, ok := transport.caller.(acceptedPluginOwner)
		if !ok {
			continue
		}
		identity, digest := owner.Incarnation(), owner.AcceptedReviewDigest()
		if identity.Validate() != nil || digest == "" {
			continue
		}
		if load, _ := m.toolLoadTypeLocked(item.uniformName); load == "opt-in" {
			continue
		}
		effect, ok := transport.ReviewedToolEffect(item.tool.Name)
		if !ok {
			continue
		}
		var declaration Tool
		for _, accepted := range transport.declarations {
			if accepted.Name == item.tool.Name {
				declaration = accepted
				break
			}
		}
		if declaration.Name == "" {
			continue
		}
		// Use accepted declarations, rather than mutable discovery/UI metadata.
		// Copy before handing data to a policy that may mutate its input.
		tool, err := cloneCatalogValue(declaration)
		if err != nil {
			m.mu.RUnlock()
			return "", nil, err
		}
		name := tool.Name
		tool.Name = item.uniformName
		selections = append(selections, catalogSelection{
			entry:     AcceptedPluginTool{Tool: tool, Effect: effect, Owner: identity, ReviewDigest: digest},
			transport: transport, name: name,
		})
	}
	m.mu.RUnlock()
	// Host load policy is external and must never run under manager locks.
	if checker != nil {
		enabled := selections[:0]
		for _, item := range selections {
			if checker.IsToolEnabled(item.entry.Tool.Name) {
				enabled = append(enabled, item)
			}
		}
		selections = enabled
	}
	sort.Slice(selections, func(i, j int) bool { return selections[i].entry.Tool.Name < selections[j].entry.Tool.Name })
	entries := make([]AcceptedPluginTool, 0, len(selections))
	for _, item := range selections {
		entries = append(entries, item.entry)
	}
	revision, err := catalogHash(entries)
	if err != nil {
		return "", nil, err
	}
	for i := range selections {
		binding, err := catalogHash(struct {
			Revision string
			Entry    AcceptedPluginTool
		}{revision, selections[i].entry})
		if err != nil {
			return "", nil, err
		}
		selections[i].entry.Binding = binding
	}
	return revision, selections, nil
}

func (c *PluginCatalog) verify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.manager == nil || c.policy == nil {
		return ErrPluginCatalogUnavailable
	}
	return c.policy.VerifyCaller(ctx)
}

func (c *PluginCatalog) List(ctx context.Context) (*AcceptedPluginCatalog, error) {
	if err := c.verify(ctx); err != nil {
		return nil, err
	}
	revision, selections, err := c.snapshot()
	if err != nil {
		return nil, err
	}
	out := &AcceptedPluginCatalog{Revision: revision, Tools: make([]AcceptedPluginTool, 0)}
	for _, selected := range selections {
		detached, snapshotErr := cloneCatalogValue(selected.entry)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		visible, policyErr := c.policy.Visible(ctx, detached)
		if policyErr != nil {
			return nil, policyErr
		}
		if visible {
			out.Tools = append(out.Tools, selected.entry)
		}
	}
	if verifyErr := c.verify(ctx); verifyErr != nil {
		return nil, verifyErr
	}
	current, _, err := c.snapshot()
	if err != nil {
		return nil, err
	}
	if current != revision {
		return nil, subprocess.ErrStaleBinding
	}
	return out, nil
}

func (c *PluginCatalog) selectCurrent(revision, name, binding string, expected *catalogSelection) (*catalogSelection, error) {
	current, selections, err := c.snapshot()
	if err != nil {
		return nil, err
	}
	if revision == "" || binding == "" || current != revision {
		return nil, subprocess.ErrStaleBinding
	}
	for i := range selections {
		item := &selections[i]
		if item.entry.Tool.Name != name {
			continue
		}
		if item.entry.Binding != binding || (expected != nil && (item.transport != expected.transport || item.name != expected.name)) {
			return nil, subprocess.ErrStaleBinding
		}
		return item, nil
	}
	return nil, subprocess.ErrStaleBinding
}

// Call retains the selected transport. The host policy owns mutation receipts
// and final commit checks; this component neither interprets operation keys nor
// retries, retargets, refreshes bindings or grants approval from catalog metadata.
func (c *PluginCatalog) Call(ctx context.Context, revision, name, binding string, args map[string]any, operationKey string) (*ToolResult, error) {
	if err := c.verify(ctx); err != nil {
		return nil, err
	}
	selected, err := c.selectCurrent(revision, name, binding, nil)
	if err != nil {
		return nil, err
	}
	arguments, err := cloneCatalogValue(args)
	if err != nil {
		return nil, err
	}
	check := func(callCtx context.Context, authorize bool) error {
		if err := c.verify(callCtx); err != nil {
			return err
		}
		detached, err := cloneCatalogValue(selected.entry)
		if err != nil {
			return err
		}
		visible, err := c.policy.Visible(callCtx, detached)
		if err != nil {
			return err
		}
		if !visible {
			return ErrPluginCatalogForbidden
		}
		// Separate copies keep callbacks from changing the eventual dispatch input.
		detached, err = cloneCatalogValue(selected.entry)
		if err != nil {
			return err
		}
		input, err := cloneCatalogValue(arguments)
		if err != nil {
			return err
		}
		if authorize {
			err = c.policy.AuthorizeCall(callCtx, detached, input, operationKey)
		} else {
			err = c.policy.RevalidateCall(callCtx, detached, input, operationKey)
		}
		if err != nil {
			return err
		}
		_, err = c.selectCurrent(revision, name, binding, selected)
		return err
	}
	if err := check(ctx, true); err != nil {
		return nil, err
	}
	ctx = subprocess.WithExpectedIncarnation(ctx, selected.entry.Owner)
	ctx = subprocess.WithDispatchValidation(ctx, func(callCtx context.Context) error { return check(callCtx, false) })
	// Also validate here for test callers or transports that do not own a manager.
	if err := check(ctx, false); err != nil {
		return nil, err
	}
	return selected.transport.CallTool(ctx, selected.name, arguments)
}
