package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestReactorPolicyResolversShareOverrideCascadeCases(t *testing.T) {
	type resolver struct {
		name          string
		metadataKey   string
		constraintKey string
		defaultPolicy string
		resolve       func(*chatServiceImpl, string) string
	}

	resolvers := []resolver{
		{
			name:          "subagent completion",
			metadataKey:   "subagent_completion_policy",
			constraintKey: "subagent_completion_policy",
			defaultPolicy: chat.SubagentPolicyRenderAndWait,
			resolve: func(svc *chatServiceImpl, sessionID string) string {
				return svc.resolveSubagentCompletionPolicy(context.Background(), sessionID)
			},
		},
		{
			name:          "message wake",
			metadataKey:   "message_wake_policy",
			constraintKey: "message_wake_policy",
			defaultPolicy: chat.SubagentPolicyAutoSummarize,
			resolve: func(svc *chatServiceImpl, sessionID string) string {
				return svc.resolveMessageWakePolicy(context.Background(), sessionID)
			},
		},
	}

	tests := []struct {
		name          string
		sessionPolicy string
		agentPolicy   string
		want          string
	}{
		{
			name:          "session override wins",
			sessionPolicy: chat.SubagentPolicyAutoSummarize,
			agentPolicy:   chat.SubagentPolicyRenderAndWait,
			want:          chat.SubagentPolicyAutoSummarize,
		},
		{
			name:        "agent default wins when session unset",
			agentPolicy: chat.SubagentPolicyBatch,
			want:        chat.SubagentPolicyBatch,
		},
		{
			name:          "invalid session override falls through to agent default",
			sessionPolicy: "auto-summarize",
			agentPolicy:   chat.SubagentPolicyRenderAndWait,
			want:          chat.SubagentPolicyRenderAndWait,
		},
		{
			name:          "invalid every tier falls back to resolver default",
			sessionPolicy: "bogus-session-policy",
			agentPolicy:   "bogus-agent-policy",
		},
		{
			name: "unset every tier falls back to resolver default",
		},
	}

	for _, r := range resolvers {
		for _, tt := range tests {
			t.Run(r.name+"/"+tt.name, func(t *testing.T) {
				want := tt.want
				if want == "" {
					want = r.defaultPolicy
				}

				svc := &chatServiceImpl{
					sessions: &stubSessionService{sessions: map[string]*store.Session{
						"sess-1": {
							ID:       "sess-1",
							Metadata: policyJSON(r.metadataKey, tt.sessionPolicy),
						},
					}},
					agents: &stubAgentService{agent: &store.AgentProfile{
						ID:          "agent-1",
						Constraints: policyJSON(r.constraintKey, tt.agentPolicy),
					}},
				}

				if got := r.resolve(svc, "sess-1"); got != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			})
		}
	}
}

func policyJSON(key, value string) string {
	if value == "" {
		return ""
	}
	return fmt.Sprintf(`{"%s":%q}`, key, value)
}
