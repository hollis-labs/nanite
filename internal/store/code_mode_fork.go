package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/structuredmessage"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

var (
	ErrCodeModeContextLimit = errors.New("code-mode context exceeds host limits")
	ErrCodeModeEscalation   = errors.New("code-mode authority overrides require an adopted host execution port")
	ErrCodeModeNested       = errors.New("nested code-mode forks are unavailable")
	ErrCodeModeSnapshot     = errors.New("code-mode parent snapshot changed")
)

// CodeModeContextLimits are supplied by the trusted host, not a profile slug or
// caller metadata. These implementation bounds are not an invocation-policy
// schema, grants, enrollment or permission to execute a child.
type CodeModeContextLimits struct {
	DefaultLastN, MaxLastN, MaxMessages, MaxBytes int
	HistoryMaxMessages, HistoryMaxBytes           int
}

func (l CodeModeContextLimits) validate() error {
	if l.DefaultLastN < 1 || l.MaxLastN < l.DefaultLastN || l.MaxLastN > 1024 || l.MaxMessages < 1 || l.MaxMessages > 4096 || l.MaxBytes < 1 || l.MaxBytes > 1<<20 || l.HistoryMaxMessages < l.MaxMessages || l.HistoryMaxMessages > 4096 || l.HistoryMaxBytes < l.MaxBytes || l.HistoryMaxBytes > 4<<20 {
		return ErrCodeModeContextLimit
	}
	return nil
}

type CodeModeForkRequest struct {
	Goal, PinnedHandoff, Scratchpad string
	Inputs                          json.RawMessage
	LastN                           int
	FullHistory                     bool
	Provider, Model                 string
	ToolGrants                      []string
	PermissionPosture, Profile      string
}

// CodeModeForkVerifier is an issuer-owned host port. The application has no production
// implementation: claimed caller strings, stored receipts and DTOs cannot
// construct it. VerifyCaller authenticates before any parent inventory read.
// Policy callbacks receive detached snapshots outside database transactions.
type CodeModeForkVerifier interface {
	VerifyCaller(context.Context, string) (CodeModeContextLimits, error)
	VerifyFork(context.Context, CodeModeParentSnapshot, CodeModeForkRequest) error
	VerifyHistory(context.Context, CodeModeHistorySnapshot, CodeModeHistoryRequest) error
}

type CodeModeForkOptions struct {
	Request  CodeModeForkRequest
	Verifier CodeModeForkVerifier
}

type CodeModeParentSnapshot struct {
	ParentViewID, Digest, ClearBoundary string
	Definition                          CognitiveViewRecord
	Session                             Session
}

type codeModeForkRecord struct {
	ParentViewID, ParentDigest string
	Limits                     CodeModeContextLimits
	Messages                   int
}

const codeModeConfigKey = "code_mode_fork"

type codeModeQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadCodeModeSnapshot(ctx context.Context, db codeModeQueryer, parentID string, limits CodeModeContextLimits) (CodeModeParentSnapshot, []Message, error) {
	var snapshot CodeModeParentSnapshot
	snapshot.ParentViewID = parentID
	var oversized bool
	if operationErr := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE session_id=? AND length(CAST(content AS BLOB))+length(CAST(COALESCE(envelope,'') AS BLOB))+length(CAST(COALESCE(metadata,'{}') AS BLOB))>? UNION ALL SELECT 1 FROM cognitive_views WHERE session_view_id=? AND length(CAST(definition_ref_json AS BLOB))+length(CAST(chat_config_json AS BLOB))>? UNION ALL SELECT 1 FROM sessions WHERE id=? AND length(CAST(metadata AS BLOB))>?)`, parentID, limits.HistoryMaxBytes, parentID, limits.HistoryMaxBytes, parentID, limits.HistoryMaxBytes).Scan(&oversized); operationErr != nil {
		return snapshot, nil, operationErr
	}
	if oversized {
		return snapshot, nil, ErrCodeModeContextLimit
	}
	var record CognitiveViewRecord
	if operationErr := db.QueryRowContext(ctx, `SELECT session_view_id,definition_ref_json,chat_config_json FROM cognitive_views WHERE session_view_id=?`, parentID).Scan(&record.SessionViewID, &record.DefinitionRefJSON, &record.ChatConfigJSON); operationErr != nil {
		return snapshot, nil, operationErr
	}
	if pinErr := verifyForkDefinition(ctx, db, record, limits.HistoryMaxBytes); pinErr != nil {
		return snapshot, nil, pinErr
	}
	var cfg map[string]json.RawMessage
	if operationErr := json.Unmarshal([]byte(record.ChatConfigJSON), &cfg); operationErr != nil {
		return snapshot, nil, operationErr
	}
	if cfg[codeModeConfigKey] != nil {
		return snapshot, nil, ErrCodeModeNested
	}
	var clearJSON string
	clearErr := db.QueryRowContext(ctx, `SELECT envelope_pointer_json FROM session_events WHERE session_id=? AND event_type='conversation_cleared' ORDER BY rowid DESC LIMIT 1`, parentID).Scan(&clearJSON)
	if clearErr != nil && !errors.Is(clearErr, sql.ErrNoRows) {
		return snapshot, nil, clearErr
	}
	if clearErr == nil {
		var boundary ConversationClear
		if operationErr := json.Unmarshal([]byte(clearJSON), &boundary); operationErr != nil || boundary.MessageID == "" {
			return snapshot, nil, ErrCodeModeSnapshot
		}
		snapshot.ClearBoundary = boundary.MessageID
	}
	snapshot.Definition = record
	snapshot.Session.ID = parentID
	if operationErr := db.QueryRowContext(ctx, `SELECT COALESCE(project_id,''),COALESCE(provider,''),COALESCE(model,''),status,metadata FROM sessions WHERE id=?`, parentID).Scan(&snapshot.Session.ProjectID, &snapshot.Session.Provider, &snapshot.Session.Model, &snapshot.Session.Status, &snapshot.Session.Metadata); operationErr != nil {
		return snapshot, nil, operationErr
	}
	var model struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if modelErr := json.Unmarshal(cfg["model"], &model); modelErr != nil || model.Provider == "" || model.Model == "" || model.Provider != snapshot.Session.Provider || model.Model != snapshot.Session.Model {
		return snapshot, nil, ErrCodeModeSnapshot
	}
	if snapshot.Session.Status == "archived" {
		return snapshot, nil, ErrCodeModeSnapshot
	}
	rows, err := db.QueryContext(ctx, `SELECT id,session_id,COALESCE(agent_id,''),role,content,COALESCE(envelope,''),COALESCE(metadata,'{}'),COALESCE(parent_id,''),is_compacted,created_at FROM messages WHERE session_id=? ORDER BY created_at,rowid LIMIT ?`, parentID, limits.HistoryMaxMessages+1)
	if err != nil {
		return snapshot, nil, err
	}
	defer closeRows(rows)
	var messages []Message
	var bytes int
	for rows.Next() {
		var m Message
		if operationErr := rows.Scan(&m.ID, &m.SessionID, &m.AgentID, &m.Role, &m.Content, &m.Envelope, &m.Metadata, &m.ParentID, &m.IsCompacted, &m.CreatedAt); operationErr != nil {
			return snapshot, nil, operationErr
		}
		bytes += len(m.ID) + len(m.Content) + len(m.Envelope) + len(m.Metadata) + len(m.ParentID) + len(m.AgentID)
		if bytes > limits.HistoryMaxBytes || len(messages) == limits.HistoryMaxMessages {
			return snapshot, nil, ErrCodeModeContextLimit
		}
		messages = append(messages, m)
	}
	if operationErr := rows.Err(); operationErr != nil {
		return snapshot, nil, operationErr
	}
	encoded, err := json.Marshal(struct {
		Snapshot CodeModeParentSnapshot
		Messages []Message
	}{snapshot, messages})
	if err != nil {
		return snapshot, nil, err
	}
	if len(encoded) > limits.HistoryMaxBytes {
		return snapshot, nil, ErrCodeModeContextLimit
	}
	digest := sha256.Sum256(encoded)
	snapshot.Digest = hex.EncodeToString(digest[:])
	return snapshot, messages, nil
}

// A pinned view must still name actual immutable installed bytes. A record
// containing a caller's claimed digest is not itself a verified definition.
func verifyForkDefinition(ctx context.Context, db codeModeQueryer, record CognitiveViewRecord, byteLimit int) error {
	var ref struct {
		ID       string `json:"definition_id"`
		Revision string `json:"revision"`
		Digest   string `json:"semantic_digest"`
	}
	if decodeErr := json.Unmarshal([]byte(record.DefinitionRefJSON), &ref); decodeErr != nil || ref.ID == "" || ref.Revision == "" || ref.Digest == "" {
		return ErrDefinitionContent
	}
	var digest, artifactDigest string
	var size int
	if readErr := db.QueryRowContext(ctx, `SELECT semantic_digest,artifact_digest,length(artifact) FROM agent_definitions WHERE definition_id=? AND revision=?`, ref.ID, ref.Revision).Scan(&digest, &artifactDigest, &size); readErr != nil {
		return readErr
	}
	if digest != ref.Digest {
		return ErrDefinitionContent
	}
	if size > byteLimit {
		return ErrCodeModeContextLimit
	}
	var artifact []byte
	if readErr := db.QueryRowContext(ctx, `SELECT artifact FROM agent_definitions WHERE definition_id=? AND revision=?`, ref.ID, ref.Revision).Scan(&artifact); readErr != nil {
		return readErr
	}
	if agentdef.ArtifactDigest(artifact) != artifactDigest {
		return ErrDefinitionContent
	}
	return nil
}

// selectCodeModeContext selects whole user-turn groups, so last-N never cuts
// between a tool call and its result. Structured assistant wrappers become
// ordinary text; envelopes, signed thinking and executable references do not
// travel with context. Historical tool output is explicitly data-only.
func selectCodeModeContext(messages []Message, request CodeModeForkRequest, limits CodeModeContextLimits) ([]Message, error) {
	n := request.LastN
	if n == 0 {
		n = limits.DefaultLastN
	}
	if n < 1 || n > limits.MaxLastN {
		return nil, ErrCodeModeContextLimit
	}
	var starts []int
	for i, m := range messages {
		if m.Role == "user" {
			starts = append(starts, i)
		}
	}
	start := len(messages)
	if len(starts) > 0 {
		start = starts[0]
		if !request.FullHistory && len(starts) > n {
			start = starts[len(starts)-n]
		}
	}
	if request.FullHistory && start != 0 && len(messages) != 0 {
		return nil, ErrCodeModeContextLimit // no orphan leading assistant/tool fragments
	}
	var selected []Message
	bytes := len(request.Goal) + len(request.Inputs) + len(request.PinnedHandoff) + len(request.Scratchpad)
	for _, original := range messages[start:] {
		if original.Role != "user" && original.Role != "assistant" && original.Role != "tool" && original.Role != "system" {
			continue
		}
		m := contextOnlyMessage(original)
		bytes += len(m.Content)
		if len(selected)+2 > limits.MaxMessages || bytes > limits.MaxBytes {
			return nil, ErrCodeModeContextLimit
		}
		selected = append(selected, m)
	}
	if bytes > limits.MaxBytes {
		return nil, ErrCodeModeContextLimit
	}
	return selected, nil
}

func contextOnlyMessage(original Message) Message {
	m := Message{ID: original.ID, Role: original.Role, Content: original.Content, Metadata: "{}", ParentID: original.ParentID}
	if text, ok := structuredmessage.UnwrapText(m.Content); ok {
		m.Content = text
	}
	if m.Role == "tool" {
		m.Role = "assistant"
		m.Content = "[Historical tool result; context only]\n" + m.Content
	}
	return m
}

// ForkCodeModeSession persists only context and the unchanged immutable pin.
// It never starts cognition, grants tools, enrolls actors, copies approval state
// or binds a fabric session. The missing verifier refuses before all effects.
func (s *Store) ForkCodeModeSession(ctx context.Context, parentID string, options CodeModeForkOptions) (*Session, error) {
	if options.Verifier == nil {
		return nil, ErrVerifiedActorRequired
	}
	limits, err := options.Verifier.VerifyCaller(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if operationErr := limits.validate(); operationErr != nil {
		return nil, operationErr
	}
	request := options.Request
	request.Inputs = append(json.RawMessage(nil), request.Inputs...)
	request.ToolGrants = append([]string(nil), request.ToolGrants...)
	if strings.TrimSpace(request.Goal) == "" || (len(request.Inputs) > 0 && !json.Valid(request.Inputs)) {
		return nil, errors.New("code-mode goal and valid JSON inputs are required")
	}
	if len(request.ToolGrants) != 0 || request.PermissionPosture != "" || request.Profile != "" {
		return nil, ErrCodeModeEscalation
	}
	before, messages, err := loadCodeModeSnapshot(ctx, s.DB, parentID, limits)
	if err != nil {
		return nil, err
	}
	if (request.Provider != "" && request.Provider != before.Session.Provider) || (request.Model != "" && request.Model != before.Session.Model) {
		return nil, ErrCodeModeEscalation
	}
	if operationErr := options.Verifier.VerifyFork(ctx, before, request); operationErr != nil {
		return nil, operationErr
	}
	// A policy callback cannot mutate these request slices into a larger copy.
	request = options.Request
	request.Inputs = append(json.RawMessage(nil), request.Inputs...)
	working := messages
	if !request.FullHistory && before.ClearBoundary != "" {
		found := false
		for i, m := range messages {
			if m.ID == before.ClearBoundary {
				working, found = messages[i+1:], true
				break
			}
		}
		if !found {
			return nil, ErrCodeModeSnapshot
		}
	}
	selected, err := selectCodeModeContext(working, request, limits)
	if err != nil {
		return nil, err
	}
	bootstrap, err := json.Marshal(struct {
		Goal          string          `json:"goal"`
		PinnedHandoff string          `json:"pinned_handoff,omitempty"`
		Scratchpad    string          `json:"scratchpad,omitempty"`
		Inputs        json.RawMessage `json:"inputs,omitempty"`
	}{request.Goal, request.PinnedHandoff, request.Scratchpad, request.Inputs})
	if err != nil {
		return nil, err
	}
	contextBytes := len(bootstrap)
	for _, m := range selected {
		contextBytes += len(m.Content)
	}
	if contextBytes > limits.MaxBytes {
		return nil, ErrCodeModeContextLimit
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackUnlessCommitted(tx)
	current, _, err := loadCodeModeSnapshot(ctx, tx, parentID, limits)
	if err != nil {
		return nil, err
	}
	if current.Digest != before.Digest {
		return nil, ErrCodeModeSnapshot
	}
	view := &Session{ID: uuid.NewString(), Provider: before.Session.Provider, Model: before.Session.Model, ProjectID: before.Session.ProjectID, Status: "active"}
	metadata, _ := json.Marshal(map[string]string{"fork_kind": "code_mode", "parent_view_id": parentID})
	view.Metadata = string(metadata)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if operationErr := tx.QueryRowContext(ctx, `INSERT INTO sessions(id,short_code,project_id,provider,model,status,metadata,subagent_runtime,message_count,last_activity,created_at,updated_at) VALUES (?,`+nextShortCodeSQL+`,?,?,?,'active',?,'api',?,?,?,?) RETURNING short_code`, view.ID, nullIfEmpty(view.ProjectID), view.Provider, view.Model, view.Metadata, len(selected)+1, now, now, now).Scan(&view.ShortCode); operationErr != nil {
		return nil, operationErr
	}
	var cfg map[string]json.RawMessage
	if operationErr := json.Unmarshal([]byte(before.Definition.ChatConfigJSON), &cfg); operationErr != nil {
		return nil, operationErr
	}
	cfg[codeModeConfigKey], err = json.Marshal(codeModeForkRecord{ParentViewID: parentID, ParentDigest: before.Digest, Limits: limits, Messages: len(messages)})
	if err != nil {
		return nil, err
	}
	config, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cognitive_views(session_view_id,definition_ref_json,chat_config_json) VALUES(?,?,?)`, view.ID, before.Definition.DefinitionRefJSON, string(config)); err != nil {
		return nil, err
	}
	copiedIDs := make(map[string]string, len(selected))
	for _, m := range selected {
		copiedIDs[m.ID] = uuid.NewString()
	}
	for _, m := range selected {
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,metadata,parent_id,created_at) VALUES(?,?,?,?,?,?,?)`, copiedIDs[m.ID], view.ID, m.Role, m.Content, "{}", nullIfEmpty(copiedIDs[m.ParentID]), now); err != nil {
			return nil, err
		}
	}
	if request.FullHistory && before.ClearBoundary != "" {
		if operationErr := copyConversationClear(ctx, tx, parentID, view.ID, copiedIDs, now); operationErr != nil {
			return nil, operationErr
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,session_id,role,content,metadata,created_at) VALUES(?,?,'user',?,'{}',?)`, uuid.NewString(), view.ID, string(bootstrap), now); err != nil {
		return nil, err
	}
	if operationErr := tx.Commit(); operationErr != nil {
		return nil, fmt.Errorf("commit code-mode context: %w", operationErr)
	}
	view.MessageCount, view.CreatedAt, view.UpdatedAt, view.LastActivity = len(selected)+1, now, now, now
	return view, nil
}
