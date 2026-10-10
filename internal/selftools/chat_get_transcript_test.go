package selftools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// This is the existing host-owned reader, not a verified actor fixture. The
// session context chooses a target; the native execution refusal is tested at
// the service boundary before this transport can be reached.
func TestChatGetHostReaderCurrentTranscript(t *testing.T) {
	s := newTestStore(t)
	st := newTestSelfToolsTransport(s)
	code := seedSessionByID(t, st, "current-transcript")
	seedSessionByID(t, st, "foreign-transcript")
	seedMessage(t, s, "current-transcript", "assistant", `{"v":1,"text":"exact old λ prose\n  ","envelopes":[{"private":"hidden"}]}`, true)
	seedMessage(t, s, "foreign-transcript", "assistant", "FOREIGN transcript", false)
	before, err := s.ListMessages(t.Context(), "current-transcript", 50)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mcp.WithSessionID(t.Context(), "current-transcript")
	res, err := st.CallTool(ctx, "chat_get", map[string]any{"limit": 1})
	if err != nil || res.IsError {
		t.Fatal("current host reader failed", res, err)
	}
	var payload struct {
		SessionID string            `json:"session_id"`
		Messages  []ChatMessageView `json:"messages"`
	}
	if json.Unmarshal([]byte(res.Content[0].Text), &payload) != nil || payload.SessionID != "current-transcript" || len(payload.Messages) != 1 || payload.Messages[0].Text != "exact old λ prose\n  " || !payload.Messages[0].IsCompacted {
		t.Fatal("compacted prose lost, unwrapped incorrectly or widened", res)
	}
	explicit, err := st.CallTool(ctx, "chat_get", map[string]any{"target": code, "limit": 1})
	if err != nil || explicit.IsError || explicit.Content[0].Text != res.Content[0].Text {
		t.Fatal("implicit target changed existing reader semantics", explicit, err)
	}
	after, err := s.ListMessages(t.Context(), "current-transcript", 50)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read changed authoritative transcript", err)
	}
}

type failingTranscriptReader struct {
	SessionReader
	pageFailure bool
	readContext context.Context
}

func (r *failingTranscriptReader) Get(ctx context.Context, id string) (*store.Session, error) {
	r.readContext = ctx
	if r.pageFailure {
		return r.SessionReader.Get(ctx, id)
	}
	return nil, errors.New("private SQL /host/database.sqlite")
}

func (r *failingTranscriptReader) GetByShortCode(ctx context.Context, _ string) (*store.Session, error) {
	r.readContext = ctx
	return nil, errors.New("private SQL /host/database.sqlite")
}

func (r *failingTranscriptReader) ListMessagesPage(ctx context.Context, _ string, _, _ int) (*store.MessagePage, error) {
	r.readContext = ctx
	return nil, errors.New("private SQL /host/database.sqlite")
}

func TestChatGetHostReaderErrorPrivacyAndContext(t *testing.T) {
	s := newTestStore(t)
	st := newTestSelfToolsTransport(s)
	seedSessionByID(t, st, "private-reader-session")
	original := st.Reads.Sessions
	ctx := mcp.WithSessionID(t.Context(), "private-reader-session")
	for _, tc := range []struct {
		name string
		args map[string]any
		page bool
	}{
		{name: "implicit"},
		{name: "explicit UUID", args: map[string]any{"target": "private-reader-session"}},
		{name: "explicit short code", args: map[string]any{"target": "#C248"}},
		{name: "page", page: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &failingTranscriptReader{SessionReader: original, pageFailure: tc.page}
			st.Reads.Sessions = reader
			res, err := st.CallTool(ctx, "chat_get", tc.args)
			if err != nil || res == nil || !res.IsError {
				t.Fatal("reader failure escaped semantic tool refusal", res, err)
			}
			if reader.readContext != ctx || strings.Contains(res.Content[0].Text, "private SQL") || strings.Contains(res.Content[0].Text, "/host/") {
				t.Fatal("read lost caller cancellation context or leaked internal cause", res)
			}
		})
	}
}

func TestChatGetHostReaderMissingContextAndPort(t *testing.T) {
	st := newSelfTools(t)
	if res, err := st.CallTool(context.Background(), "chat_get", nil); err != nil || !res.IsError {
		t.Fatal("missing target/context became access", res, err)
	}
	st.Reads.Sessions = nil
	ctx := mcp.WithSessionID(t.Context(), "claimed session")
	if res, err := st.CallTool(ctx, "chat_get", nil); err != nil || !res.IsError {
		t.Fatal("missing host reader became access", res, err)
	}
	if got := extractMessageText(`{"v":1,"text":"","envelopes":[{"private":"hidden"}]}`); got != "" {
		t.Fatal("empty assistant prose leaked wrapper", got)
	}
}
