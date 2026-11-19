// Package recovery reconciles in-flight work after taking the agent's lock.
package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
)

func Recover(ctx context.Context, db *store.Store) error {
	turns, err := db.RunningTurns(ctx)
	if err != nil {
		return err
	}
	for _, turn := range turns {
		journal, err := db.OpenJournal(ctx, turn.ConversationID)
		if err != nil {
			return err
		}
		if err := errors.Join(journal.InterruptTurn(turn.TurnID, time.Now().UTC()), journal.Close()); err != nil {
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
		job.Status = "lost"
		notice := store.Notice{ID: store.NewID(time.Now()), ConversationID: job.ConversationID, Kind: job.Kind,
			Body: fmt.Sprintf("%s job %s was interrupted by a process exit", job.Kind, job.ID)}
		if err := db.CompleteJob(ctx, job, notice); err != nil {
			return err
		}
	}
	return nil
}

const SyntheticResult = "interrupted before completion; not retried"

type Plan struct {
	UserText      string
	AssistantText string
	Steps         int
	Missing       []llmtypes.ContentBlock
	Generate      bool
}

func ResumePlan(history []store.Message, turn store.InterruptedTurn) (Plan, error) {
	var out Plan
	var userFound bool
	var calls []string
	results := make(map[string]bool)
	sawSynthetic := false
	for _, m := range history {
		if m.TurnID != turn.ID {
			continue
		}
		switch m.Role {
		case "user":
			if m.ID == turn.UserMessageID {
				userFound = true
				for _, b := range m.Blocks {
					if b.Type == "text" {
						out.UserText += b.Text
					}
				}
			}
		case "assistant":
			out.Steps++
			sawSynthetic = false
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
