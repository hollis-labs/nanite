package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// The submitting credential of a native turn rides the request context into the
// admission call and is copied (as an id only) into the detached generation
// context. It is looked up again on every dispatch; the id is never a verdict.
type submittedByKey struct{}

// WithSubmittedBy records which credential submitted the turn.
func WithSubmittedBy(ctx context.Context, credentialID string) context.Context {
	return context.WithValue(ctx, submittedByKey{}, credentialID)
}

func submittedByFrom(ctx context.Context) string {
	id, _ := ctx.Value(submittedByKey{}).(string)
	return id
}

// SubmittedByFromContext returns the id recorded by WithSubmittedBy. It is an
// identifier for audit and tests, never an authorization.
func SubmittedByFromContext(ctx context.Context) string { return submittedByFrom(ctx) }

func copySubmittedBy(from, into context.Context) context.Context {
	if id := submittedByFrom(from); id != "" {
		return WithSubmittedBy(into, id)
	}
	return into
}

// nativeToolGate adapts a live capability to toolclient.NativeToolGate.
type nativeToolGate struct {
	grants *NativeGrants
	cap    *executionCapability
}

func (g nativeToolGate) AuthorizeNativeTool(ctx context.Context, toolName string) (bool, string) {
	return g.grants.AuthorizeCapability(ctx, g.cap, toolName)
}

// viewHostSettingsRef reads the host settings reference a native view was
// admitted under. Views created before it was recorded have none and run
// tool-free.
func (s *chatServiceImpl) viewHostSettingsRef(ctx context.Context, viewID string) (HostSettingsRef, bool) {
	reader, ok := s.store.(interface {
		GetCognitiveView(context.Context, string) (store.CognitiveViewRecord, error)
	})
	if !ok {
		return HostSettingsRef{}, false
	}
	record, err := reader.GetCognitiveView(ctx, viewID)
	if err != nil {
		return HostSettingsRef{}, false
	}
	var cfg struct {
		Ref *HostSettingsRef `json:"host_settings_ref"`
	}
	if json.Unmarshal([]byte(record.ChatConfigJSON), &cfg) != nil || cfg.Ref == nil || cfg.Ref.ID == "" || cfg.Ref.Revision == "" {
		return HostSettingsRef{}, false
	}
	return *cfg.Ref, true
}

// issueNativeCapability starts a native turn's execution capability when the
// view has a recorded host settings reference, the turn has a submitting
// credential, and native grants are wired. It returns the context to run the
// turn in (carrying the capability and the tool gate) and the function that
// ends the capability. Otherwise it returns ctx unchanged and a no-op, and the
// turn stays tool-free exactly as before.
func (s *chatServiceImpl) issueNativeCapability(ctx context.Context, viewID, turnID string) (context.Context, func()) {
	noop := func() {}
	if s.nativeGrants == nil || s.capabilities == nil || !isCognitiveTurn(ctx) {
		return ctx, noop
	}
	by := submittedByFrom(ctx)
	ref, ok := s.viewHostSettingsRef(ctx, viewID)
	if by == "" || !ok {
		return ctx, noop
	}
	c, err := s.capabilities.issueRoot(viewID, turnID, turnID, ref.ID, ref.Revision, by, nil)
	if err != nil {
		slog.Warn("native-grants: no capability issued", "view", viewID, "err", err)
		return ctx, noop
	}
	ctx = withExecutionCapability(ctx, c)
	ctx = toolclient.WithNativeToolGate(ctx, nativeToolGate{grants: s.nativeGrants, cap: c})
	return ctx, c.end
}

// nativeOfferedTools returns the definitions of the tools a native turn may be
// offered right now: the live capability's grants, intersected with the turn's
// narrowing and the submitting credential's standing. A turn without a live capability gets
// none.
func (s *chatServiceImpl) nativeOfferedTools(ctx context.Context) []llmtypes.ToolDefinition {
	c, ok := executionCapabilityFrom(ctx)
	if !ok || s.nativeGrants == nil {
		return nil
	}
	names := s.nativeGrants.Offered(ctx, NativeSubject{HostSettingsID: c.hostSettingsID, AdmittedRevision: c.admittedRevision, SubmittedBy: c.submittedBy}, c.narrowing)
	if len(names) == 0 {
		return nil
	}
	defs, ok := s.tools.(interface {
		NativeToolDefinitions(names []string) []llmtypes.ToolDefinition
	})
	if !ok {
		return nil
	}
	return defs.NativeToolDefinitions(names)
}
