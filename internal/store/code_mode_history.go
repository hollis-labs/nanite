package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type CodeModeHistoryRequest struct {
	Query, Cursor string
	Limit         int
}

type CodeModeHistorySnapshot struct {
	ForkViewID string
	Parent     CodeModeParentSnapshot
}

type CodeModeHistoryMatch struct {
	ParentViewID, MessageID, Role, Content string
}

type CodeModeHistoryPage struct {
	Matches        []CodeModeHistoryMatch
	NextCursor     string
	SnapshotDigest string
}

type codeModeHistoryCursor struct {
	ForkViewID, ParentDigest, QueryDigest string
	Offset                                int
}

// SearchCodeModeParentHistory is an internal, bounded read primitive. No tool
// is registered until a real issuer-owned verifier is supplied. Provenance is
// read from the private pinned view record, never mutable session metadata.
// Any parent change invalidates this snapshot; pages never widen to live data.
func (s *Store) SearchCodeModeParentHistory(ctx context.Context, forkID string, request CodeModeHistoryRequest, verifier CodeModeForkVerifier) (CodeModeHistoryPage, error) {
	var page CodeModeHistoryPage
	if verifier == nil {
		return page, ErrVerifiedActorRequired
	}
	limits, err := verifier.VerifyCaller(ctx, forkID)
	if err != nil {
		return page, err
	}
	if operationErr := limits.validate(); operationErr != nil {
		return page, operationErr
	}
	if strings.TrimSpace(request.Query) == "" || len(request.Query) > 256 || len(request.Cursor) > 2048 || request.Limit < 1 || request.Limit > limits.MaxMessages {
		return page, ErrCodeModeContextLimit
	}
	fork, err := s.GetCognitiveView(ctx, forkID)
	if err != nil {
		return page, err
	}
	var config map[string]json.RawMessage
	if operationErr := json.Unmarshal([]byte(fork.ChatConfigJSON), &config); operationErr != nil {
		return page, operationErr
	}
	var provenance codeModeForkRecord
	if operationErr := json.Unmarshal(config[codeModeConfigKey], &provenance); operationErr != nil || provenance.ParentViewID == "" || provenance.ParentDigest == "" {
		return page, ErrCodeModeSnapshot
	}
	if operationErr := provenance.Limits.validate(); operationErr != nil {
		return page, operationErr
	}
	// The current host may narrow the old limits, but cannot widen the fork's
	// accepted ceiling by returning a more permissive policy on reconnect.
	if request.Limit > provenance.Limits.MaxMessages {
		return page, ErrCodeModeContextLimit
	}
	historyLimits := provenance.Limits
	historyLimits.HistoryMaxMessages = min(historyLimits.HistoryMaxMessages, limits.HistoryMaxMessages)
	historyLimits.HistoryMaxBytes = min(historyLimits.HistoryMaxBytes, limits.HistoryMaxBytes)
	parent, messages, err := loadCodeModeSnapshot(ctx, s.DB, provenance.ParentViewID, historyLimits)
	if err != nil {
		return page, err
	}
	if parent.Digest != provenance.ParentDigest || len(messages) != provenance.Messages || parent.Definition.DefinitionRefJSON != fork.DefinitionRefJSON {
		return page, ErrCodeModeSnapshot
	}
	if len(messages) > limits.HistoryMaxMessages {
		return page, ErrCodeModeContextLimit
	}
	if operationErr := verifier.VerifyHistory(ctx, CodeModeHistorySnapshot{ForkViewID: forkID, Parent: parent}, request); operationErr != nil {
		return page, operationErr
	}
	queryDigest := sha256.Sum256([]byte(request.Query))
	cursor := codeModeHistoryCursor{ForkViewID: forkID, ParentDigest: parent.Digest, QueryDigest: hex.EncodeToString(queryDigest[:])}
	if request.Cursor != "" {
		encoded, decodeErr := base64.RawURLEncoding.DecodeString(request.Cursor)
		if decodeErr != nil || json.Unmarshal(encoded, &cursor) != nil || cursor.ForkViewID != forkID || cursor.ParentDigest != parent.Digest || cursor.QueryDigest != hex.EncodeToString(queryDigest[:]) || cursor.Offset < 0 || cursor.Offset > len(messages) {
			return page, errors.New("invalid code-mode parent-history cursor")
		}
	}
	// Re-read under a read transaction after host policy returns. No callback
	// runs under database locks, and callback-time mutation cannot leak data.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return page, err
	}
	defer rollbackUnlessCommitted(tx)
	current, _, err := loadCodeModeSnapshot(ctx, tx, provenance.ParentViewID, historyLimits)
	if err != nil {
		return page, err
	}
	var currentFork CognitiveViewRecord
	if operationErr := tx.QueryRowContext(ctx, `SELECT session_view_id,definition_ref_json,chat_config_json FROM cognitive_views WHERE session_view_id=?`, forkID).Scan(&currentFork.SessionViewID, &currentFork.DefinitionRefJSON, &currentFork.ChatConfigJSON); operationErr != nil {
		return page, operationErr
	}
	if current.Digest != parent.Digest || currentFork != fork {
		return page, ErrCodeModeSnapshot
	}
	var bytes int
	byteLimit := min(limits.MaxBytes, provenance.Limits.MaxBytes)
	query := strings.ToLower(request.Query)
	page.Matches = make([]CodeModeHistoryMatch, 0)
	for i := cursor.Offset; i < len(messages); i++ {
		m := contextOnlyMessage(messages[i])
		if !strings.Contains(strings.ToLower(m.Content), query) {
			continue
		}
		match := CodeModeHistoryMatch{ParentViewID: provenance.ParentViewID, MessageID: m.ID, Role: m.Role, Content: m.Content}
		encoded, marshalErr := json.Marshal(match)
		if marshalErr != nil {
			return CodeModeHistoryPage{}, marshalErr
		}
		if bytes+len(encoded) > byteLimit {
			if len(page.Matches) == 0 {
				return CodeModeHistoryPage{}, ErrCodeModeContextLimit
			}
			cursor.Offset = i
			break
		}
		bytes += len(encoded)
		page.Matches = append(page.Matches, match)
		cursor.Offset = i + 1
		if len(page.Matches) == request.Limit {
			break
		}
		if i == len(messages)-1 {
			cursor.Offset = len(messages)
		}
	}
	// No more matching messages means no cursor, even when trailing rows do
	// not match. A cursor is a scoped selector, never caller identity.
	var remaining bool
	for _, m := range messages[cursor.Offset:] {
		if strings.Contains(strings.ToLower(contextOnlyMessage(m).Content), query) {
			remaining = true
			break
		}
	}
	if remaining {
		encoded, err := json.Marshal(cursor)
		if err != nil {
			return CodeModeHistoryPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	page.SnapshotDigest = parent.Digest
	return page, tx.Commit()
}
