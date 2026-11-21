// Package recovery reconciles in-flight work after taking the agent's lock.
package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/integrity"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/transcript"
)

// Disposition is what recovery did with a conversation it checked.
type Disposition string

const (
	// Resumable: the interrupted turn may be resumed or discarded.
	Resumable Disposition = "resumable"
	// Blocked: an unsafe finding refuses Resume. Discard stays available
	// unless the transcript itself refuses writes (a torn tail).
	Blocked Disposition = "blocked"
)

// Check is one conversation recovery validated before touching it.
type Check struct {
	Conversation string
	Turns        []string // the running turns it found there
	Report       integrity.Report
	Disposition  Disposition
}

// DispositionOf is the disposition a report allows.
func DispositionOf(report integrity.Report) Disposition {
	if report.Safe() {
		return Resumable
	}
	return Blocked
}

// Recover reconciles what the previous process left in flight, after the
// agent lock is held. Candidates are bounded: only conversations with a
// running turn are validated, never the whole history. Each is checked
// before its turns are marked interrupted; observe, if set, receives every
// check. An unsafe conversation is still marked interrupted, so Discard can
// close it, unless its transcript refuses writes (a torn tail): then its
// turn stays running and Resume reports why.
func Recover(ctx context.Context, db *store.Store, observe func(Check)) error {
	turns, err := db.RunningTurns(ctx)
	if err != nil {
		return err
	}
	var order []string
	running := map[string][]string{}
	for _, turn := range turns {
		if running[turn.ConversationID] == nil {
			order = append(order, turn.ConversationID)
		}
		running[turn.ConversationID] = append(running[turn.ConversationID], turn.TurnID)
	}
	for _, conversation := range order {
		report, err := db.ValidateConversation(ctx, conversation, nil)
		if err != nil {
			return fmt.Errorf("validate conversation %s: %w", conversation, err)
		}
		check := Check{Conversation: conversation, Turns: running[conversation], Report: report, Disposition: DispositionOf(report)}
		if observe != nil {
			observe(check)
		}
		journal, err := db.OpenJournal(ctx, conversation)
		if errors.Is(err, transcript.ErrTorn) && check.Disposition == Blocked {
			continue
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, turn := range check.Turns {
			if err = journal.InterruptTurn(turn, now); err != nil {
				break
			}
		}
		if err := errors.Join(err, journal.Close()); err != nil {
			return err
		}
	}
	jobs, err := db.RunningJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := shell.TerminateRecordedGroup(ctx, job.PID, job.ProcessIdentity); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("terminate job %s: %w", job.ID, err)
		}
		job.Status, job.Reason = "lost", store.JobReasonProcessExit
		notice := store.Notice{ID: store.NewID(time.Now()), ConversationID: job.ConversationID, Kind: job.Kind,
			Body: fmt.Sprintf("%s job %s was interrupted by a process exit", job.Kind, job.ID)}
		if err := db.CompleteJob(ctx, job, notice); err != nil {
			return err
		}
	}
	return nil
}

// SyntheticResult answers a tool call an interrupted turn never answered.
const SyntheticResult = integrity.SyntheticResult

// ClosingPromptPrefix marks a persisted closing prompt. It records the limit
// reason and separates the closing reply from counted tool-capable steps.
const ClosingPromptPrefix = integrity.ClosingPromptPrefix

// IsClosingPrompt identifies the engine's persisted host note. It uses block
// metadata, never text matching, so a person quoting the prompt stays visible.
// The user role is retained in storage for provider and recovery compatibility.
func IsClosingPrompt(role string, blocks []llmtypes.ContentBlock) bool {
	return role == "user" && slices.ContainsFunc(blocks, func(b llmtypes.ContentBlock) bool {
		return b.Type == "text" && strings.HasPrefix(b.Name, ClosingPromptPrefix)
	})
}

type Plan struct {
	ClosingReason string
	UserText      string
	AssistantText string
	Steps         int
	Missing       []llmtypes.ContentBlock
	Generate      bool
}

func ResumePlan(history []store.Message, turn store.InterruptedTurn) (Plan, error) {
	var out Plan
	var userFound bool
	// Calls pair with results per step: a model step's calls are answered by
	// the tool results after it, before the next step. Providers that number
	// calls per response reuse an ID in a later step, so matching by ID
	// across the turn would take a reused, unanswered call as answered.
	// Only the last step can still be answered: its pending calls are what
	// the turn is missing.
	var calls []string               // the last step's calls, in order
	results := make(map[string]bool) // which of them have a result
	sawSynthetic := false
	for _, m := range history {
		if m.TurnID != turn.ID {
			continue
		}
		switch m.Role {
		case "user":
			for _, b := range m.Blocks {
				if strings.HasPrefix(b.Name, ClosingPromptPrefix) {
					out.ClosingReason = strings.TrimPrefix(b.Name, ClosingPromptPrefix)
				}
			}
			if m.ID == turn.UserMessageID {
				userFound = true
				for _, b := range m.Blocks {
					if b.Type == "text" && b.Name != slots.TurnContextBlock {
						out.UserText += b.Text
					}
				}
			}
		case "assistant":
			if out.ClosingReason == "" {
				out.Steps++
			}
			sawSynthetic = false
			calls = calls[:0]
			clear(results)
			for _, b := range m.Blocks {
				if b.Type == "text" {
					out.AssistantText += b.Text
				}
				if b.Type == "tool_use" {
					calls = append(calls, b.ID)
				}
			}
		case "tool":
			for _, b := range m.Blocks {
				if b.Type == "tool_result" {
					results[b.ToolUseID] = true
					sawSynthetic = sawSynthetic || b.IsError && b.Content == SyntheticResult
				}
			}
		}
	}
	if !userFound {
		return out, fmt.Errorf("interrupted turn has no stored user message")
	}
	for _, id := range calls {
		if !results[id] {
			out.Missing = append(out.Missing, llmtypes.ContentBlock{Type: "tool_result", ToolUseID: id, Content: SyntheticResult, IsError: true})
		}
	}
	out.Generate = out.Steps == 0 || len(out.Missing) > 0 || sawSynthetic
	return out, nil
}
