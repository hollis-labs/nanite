package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// Optional ports keep existing SessionWriter and SessionService adapters
// unchanged. Supplying a verifier is a host integration requirement, not a
// public issuer or a newly enrolled execution identity.
type codeModeForkWriter interface {
	ForkCodeModeSession(context.Context, string, store.CodeModeForkOptions) (*store.Session, error)
}

type codeModeParentHistoryReader interface {
	SearchCodeModeParentHistory(context.Context, string, store.CodeModeHistoryRequest, store.CodeModeForkVerifier) (store.CodeModeHistoryPage, error)
}

// SearchCodeModeParentHistory is callable only through an actual host-owned
// verifier. This source does not register a tool or expose global transcripts.
func SearchCodeModeParentHistory(ctx context.Context, backing any, forkID string, request store.CodeModeHistoryRequest, verifier store.CodeModeForkVerifier) (store.CodeModeHistoryPage, error) {
	reader, ok := backing.(codeModeParentHistoryReader)
	if verifier == nil || !ok {
		return store.CodeModeHistoryPage{}, store.ErrVerifiedActorRequired
	}
	return reader.SearchCodeModeParentHistory(ctx, forkID, request, verifier)
}
