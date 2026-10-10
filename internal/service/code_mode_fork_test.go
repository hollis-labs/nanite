package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

type codeModeForkDispatchProbe struct {
	SessionWriter
	legacyCalls, codeCalls int
	parent                 string
	options                store.CodeModeForkOptions
}

func (p *codeModeForkDispatchProbe) ForkSession(context.Context, string, *store.Session, bool) (*store.Session, error) {
	p.legacyCalls++
	return nil, errors.New("unexpected legacy fork")
}
func (p *codeModeForkDispatchProbe) ForkCodeModeSession(_ context.Context, parent string, options store.CodeModeForkOptions) (*store.Session, error) {
	p.codeCalls++
	p.parent, p.options = parent, options
	return nil, store.ErrVerifiedActorRequired // No real issuer exists in this probe.
}

type codeModeServiceVerifier struct{}

func (codeModeServiceVerifier) VerifyCaller(context.Context, string) (store.CodeModeContextLimits, error) {
	return store.CodeModeContextLimits{}, store.ErrVerifiedActorRequired
}
func (codeModeServiceVerifier) VerifyFork(context.Context, store.CodeModeParentSnapshot, store.CodeModeForkRequest) error {
	return store.ErrVerifiedActorRequired
}
func (codeModeServiceVerifier) VerifyHistory(context.Context, store.CodeModeHistorySnapshot, store.CodeModeHistoryRequest) error {
	return store.ErrVerifiedActorRequired
}

func TestCodeModeForkServiceOptionalPortRefusesWithoutLegacyEffects(t *testing.T) {
	probe := &codeModeForkDispatchProbe{}
	svc := NewSessionService(SessionServiceDeps{Writer: probe})
	for _, opts := range []ForkOpts{{ForkKind: "code_mode"}, {ForkKind: "code_mode", CodeMode: &store.CodeModeForkOptions{Request: store.CodeModeForkRequest{Goal: "claimed"}}}} {
		if view, err := svc.Fork(t.Context(), "claimed-parent", opts); view != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("unsupported host path admitted: %+v,%v", view, err)
		}
	}
	if probe.legacyCalls != 0 || probe.codeCalls != 0 {
		t.Fatalf("missing verifier dispatched: %+v", probe)
	}
	options := &store.CodeModeForkOptions{Verifier: codeModeServiceVerifier{}, Request: store.CodeModeForkRequest{Goal: "exact request", LastN: 2}}
	view, err := svc.Fork(t.Context(), "parent", ForkOpts{ForkKind: "code_mode", CodeMode: options, Provider: "host-selected", Model: "model"})
	if view != nil || !errors.Is(err, store.ErrVerifiedActorRequired) || probe.legacyCalls != 0 || probe.codeCalls != 1 || probe.parent != "parent" || probe.options.Request.Provider != "host-selected" || probe.options.Request.Model != "model" || probe.options.Request.Goal != "exact request" || options.Request.Provider != "" || options.Request.Model != "" {
		t.Fatalf("optional dispatch changed caller or bypassed refusal: %+v,%v probe=%+v", view, err, probe)
	}
	for _, opts := range []ForkOpts{{ForkKind: "other"}, {CodeMode: options}} {
		if view, err := svc.Fork(t.Context(), "parent", opts); view != nil || !errors.Is(err, store.ErrCodeModeEscalation) {
			t.Fatalf("ambiguous fork kind admitted: %+v,%v", view, err)
		}
	}
	if probe.codeCalls != 1 || probe.legacyCalls != 0 {
		t.Fatalf("ambiguous operation dispatched: %+v", probe)
	}
}

func TestCodeModeParentHistoryMissingPortDoesNotUseGlobalSearch(t *testing.T) {
	for _, verifier := range []store.CodeModeForkVerifier{nil, codeModeServiceVerifier{}} {
		page, err := SearchCodeModeParentHistory(t.Context(), nil, "claimed", store.CodeModeHistoryRequest{Query: "private", Limit: 1}, verifier)
		if !errors.Is(err, store.ErrVerifiedActorRequired) || len(page.Matches) != 0 {
			t.Fatalf("missing scoped reader used fallback: %+v,%v", page, err)
		}
	}
}
