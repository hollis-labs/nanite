package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// newWorkflowContextAssemblerTestDeps stands up a real in-memory store plus
// SessionService/AgentService/ContextService backed by it — mirroring how
// container.go wires the same trio in production — so these tests exercise
// the actual resolution path rather than a mocked stand-in.
func newWorkflowContextAssemblerTestDeps(t *testing.T) (*store.Store, SessionService, AgentService, ContextService) {
	t.Helper()
	s, err := storetest.New(t, context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sessions := NewSessionService(SessionServiceDeps{
		Sessions:    s,
		Writer:      s,
		Agents:      s,
		AgentReader: s,
		Settings:    s,
	})
	agents := NewAgentService(AgentServiceConfig{
		Agents:   s,
		Writers:  s,
		Settings: s,
	})
	ctxSvc := NewContextService(ContextServiceConfig{Client: chat.NewContextClient(s)})
	return s, sessions, agents, ctxSvc
}

// Phase 0 item 21 ("Cut Modes, in full") retired this file's four original
// mode-addendum tests —
// TestWorkflowContextAssembler_AssembleContext_IncludesModeAddendumWhenAgentMatchesSessionBinding,
// TestWorkflowContextAssembler_AssembleContext_SkipsModeWhenBoundToADifferentAgent,
// TestWorkflowContextAssembler_AssembleContext_NoSessionBinding_NoModeAddendum, and
// TestWorkflowContextAssembler_AssembleContext_IncludesSessionLevelModeAddendum —
// along with resolveMode / resolveSessionMode in workflow_context_assembler.go
// (both deleted; see that file's updated doc comment). All four asserted
// something about a mode's PromptAddendum reaching (or being correctly
// excluded from) the assembled system prompt; Legacy Agent Mode and Session
// Mode are both gone, so there is no more mode addendum to include or
// exclude. TestWorkflowContextAssembler_AssembleContext_ResolvesAgentAndSession
// below replaces them with coverage of what AssembleContext still does:
// resolve the named session + agent and return a non-error system prompt
// carrying the agent's own SystemPrompt.

func TestWorkflowContextAssembler_AssembleContext_ResolvesAgentAndSession(t *testing.T) {
	s, sessions, agents, ctxSvc := newWorkflowContextAssemblerTestDeps(t)

	agentProfile := &store.AgentProfile{
		ID:           "agent-workflow-1",
		Name:         "WorkflowAgent",
		Slug:         "workflow-agent",
		SystemPrompt: "SENTINEL_AGENT_SYSTEM_PROMPT",
		Status:       "active",
	}
	if err := persistTestActor(context.Background(), s, agentProfile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	sess := &store.Session{ID: "sess-workflow-1", Title: "test"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.EnsureSessionAgent(context.Background(), sess.ID, agentProfile.ID, "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	asm := NewWorkflowContextAssembler(sessions, agents, s, ctxSvc)
	systemPrompt, _, err := asm.AssembleContext(context.Background(), sess.ID, agentProfile.ID)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if !strings.Contains(systemPrompt, "SENTINEL_AGENT_SYSTEM_PROMPT") {
		t.Fatalf("expected assembled system prompt to include the agent's SystemPrompt, got: %q", systemPrompt)
	}
}

// TestWorkflowContextAssembler_AssembleContext_NoSessionBinding confirms
// AssembleContext refuses a missing prior session binding; neither a
// supplied actor URI nor a host ID can enroll it as a side effect.
func TestWorkflowContextAssembler_AssembleContext_NoSessionBinding(t *testing.T) {
	s, sessions, agents, ctxSvc := newWorkflowContextAssemblerTestDeps(t)

	agentProfile := &store.AgentProfile{ID: "agent-unbound", Name: "Unbound", Slug: "unbound", SystemPrompt: "unbound agent", Status: "active"}
	if err := persistTestActor(context.Background(), s, agentProfile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	sess := &store.Session{ID: "sess-mode-3", Title: "test"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// No EnsureSessionAgent call — the session has no primary-agent binding.

	asm := NewWorkflowContextAssembler(sessions, agents, s, ctxSvc)
	if _, _, err := asm.AssembleContext(context.Background(), sess.ID, agentProfile.ID); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("unbound session admitted: %v", err)
	}
}

// The actor and matching session fence runs before session inventory/assembly.
type workflowAdmissionSessionCounter struct {
	SessionService
	reads int
}

func (s *workflowAdmissionSessionCounter) Get(context.Context, string) (*store.Session, error) {
	s.reads++
	return nil, errors.New("session inventory must not be reached")
}
func TestWorkflowContextAssemblerRefusesUnverifiedOrForeignBindingBeforeReads(t *testing.T) {
	for _, kind := range []string{"host-id", "disabled", "empty-receipt", "unbound-session", "different-actor"} {
		t.Run(kind, func(t *testing.T) {
			st, sessions, agents, ctxSvc := newWorkflowContextAssemblerTestDeps(t)
			p := &store.AgentProfile{Name: "Prior workflow", Slug: "prior-workflow-admission"}
			if err := persistTestActor(t.Context(), st, p); err != nil {
				t.Fatal(err)
			}
			sess := &store.Session{ID: "private-admission-session"}
			if err := st.CreateSession(t.Context(), sess); err != nil {
				t.Fatal(err)
			}
			if kind != "unbound-session" {
				if err := st.EnsureSessionAgent(t.Context(), sess.ID, p.ID, "", true); err != nil {
					t.Fatal(err)
				}
			}
			actor := p.ID
			switch kind {
			case "host-id":
				if err := st.DB.QueryRowContext(t.Context(), `SELECT host_settings_id FROM agent_actor_bindings WHERE actor_uri=?`, p.ID).Scan(&actor); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`, p.ID); err != nil {
					t.Fatal(err)
				}
			case "empty-receipt":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_actor_bindings SET binding_receipt='' WHERE actor_uri=?`, p.ID); err != nil {
					t.Fatal(err)
				}
			case "different-actor":
				other := &store.AgentProfile{Name: "Other prior", Slug: "other-prior-workflow"}
				if err := persistTestActor(t.Context(), st, other); err != nil {
					t.Fatal(err)
				}
				actor = other.ID
			}
			counter := &workflowAdmissionSessionCounter{SessionService: sessions}
			assembler := NewWorkflowContextAssembler(counter, agents, st, ctxSvc)
			prompt, messages, err := assembler.AssembleContext(t.Context(), sess.ID, actor)
			if !errors.Is(err, store.ErrVerifiedActorRequired) || prompt != "" || len(messages) != 0 || counter.reads != 0 {
				t.Fatalf("admission reached session/context effects: %q %v %v reads=%d", prompt, messages, err, counter.reads)
			}
		})
	}
}
