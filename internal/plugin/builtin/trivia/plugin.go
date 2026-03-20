package trivia

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("trivia", func() plugin.Plugin { return New() })
}

// TriviaPlugin provides an interactive trivia game via chat commands.
// Users trigger it with "!trivia" to get a question, "!answer A/B/C/D"
// to answer, and "!scores" to view the leaderboard.
type TriviaPlugin struct {
	host   plugin.Host
	status plugin.PluginStatus
}

// New creates a new TriviaPlugin instance.
func New() *TriviaPlugin {
	return &TriviaPlugin{}
}

func (p *TriviaPlugin) ID() string          { return "trivia" }
func (p *TriviaPlugin) Name() string        { return "Trivia Game" }
func (p *TriviaPlugin) Version() string     { return "0.1.0" }
func (p *TriviaPlugin) Description() string { return "Interactive trivia game — !trivia, !answer, !scores" }
func (p *TriviaPlugin) Dependencies() []string { return nil }

func (p *TriviaPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Check for offline/demo mode preference.
	demoMode := os.Getenv("TRIVIA_DEMO_MODE")

	// Register event hook for message.sent — intercepts !trivia, !answer, !scores.
	hook := &triviaEventHook{
		logger:   logger,
		scores:   NewScoreTracker(),
		demoMode: demoMode == "1" || demoMode == "true",
	}
	if err := host.RegisterEventHook([]string{"message.sent"}, hook); err != nil {
		return fmt.Errorf("failed to register event hook: %w", err)
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("trivia plugin loaded", "version", p.Version())
	return nil
}

func (p *TriviaPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("trivia plugin unloaded")
	}
	return nil
}

func (p *TriviaPlugin) Status() plugin.PluginStatus {
	return p.status
}

// triviaEventHook intercepts trivia commands from chat messages.
type triviaEventHook struct {
	logger   plugin.Logger
	scores   *ScoreTracker
	demoMode bool
	// pending tracks the current question per session for answer validation.
	pending map[string]*TriviaQuestion
}

func (h *triviaEventHook) EventTypes() []string {
	return []string{"message.sent"}
}

func (h *triviaEventHook) Handle(ctx context.Context, event plugin.Event) error {
	content, _ := event.Data["content"].(string)
	if content == "" {
		return nil
	}

	trimmed := strings.TrimSpace(content)

	if trimmed == "!trivia" {
		return h.handleTrivia(ctx, event)
	}

	if strings.HasPrefix(trimmed, "!answer ") {
		answer := strings.TrimSpace(strings.TrimPrefix(trimmed, "!answer"))
		return h.handleAnswer(ctx, event, answer)
	}

	if trimmed == "!scores" {
		return h.handleScores(event)
	}

	return nil
}

func (h *triviaEventHook) handleTrivia(ctx context.Context, event plugin.Event) error {
	h.logger.Info("trivia question requested", "session", event.SessionID)

	var question *TriviaQuestion
	var err error

	if h.demoMode {
		question = GetDemoQuestion()
	} else {
		question, err = FetchQuestion(ctx)
		if err != nil {
			h.logger.Warn("trivia API failed, falling back to demo", "error", fmt.Sprintf("%v", err))
			question = GetDemoQuestion()
		}
	}

	if question == nil {
		return nil
	}

	// Store the pending question for this session.
	if h.pending == nil {
		h.pending = make(map[string]*TriviaQuestion)
	}
	h.pending[event.SessionID] = question

	envData := map[string]any{
		"question":       question.Question,
		"category":       question.Category,
		"difficulty":     question.Difficulty,
		"answers":        question.Answers,
		"correct_index":  question.CorrectIndex,
		"source":         question.Source,
	}
	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "trivia-question",
		"data":    envData,
	})

	event.Data["envelope"] = string(envJSON)
	h.logger.Debug("trivia-question envelope attached", "category", question.Category)
	return nil
}

func (h *triviaEventHook) handleAnswer(_ context.Context, event plugin.Event, answer string) error {
	if h.pending == nil {
		return nil
	}

	question, ok := h.pending[event.SessionID]
	if !ok || question == nil {
		return nil
	}

	// Map letter to index: A=0, B=1, C=2, D=3
	answer = strings.ToUpper(strings.TrimSpace(answer))
	answerIdx := -1
	if len(answer) == 1 && answer[0] >= 'A' && answer[0] <= 'D' {
		answerIdx = int(answer[0] - 'A')
	}

	if answerIdx < 0 || answerIdx >= len(question.Answers) {
		return nil
	}

	correct := answerIdx == question.CorrectIndex
	userID := event.SessionID // Use session as user identifier for now.

	if correct {
		// Award points based on difficulty.
		points := 1
		switch question.Difficulty {
		case "medium":
			points = 2
		case "hard":
			points = 3
		}
		h.scores.AddScore(userID, points)
	}

	// Clear pending question.
	delete(h.pending, event.SessionID)

	// Build a result envelope embedded in a trivia-question response.
	envData := map[string]any{
		"question":        question.Question,
		"category":        question.Category,
		"difficulty":      question.Difficulty,
		"answers":         question.Answers,
		"correct_index":   question.CorrectIndex,
		"selected_index":  answerIdx,
		"correct":         correct,
		"score":           h.scores.GetScore(userID),
		"source":          question.Source,
	}
	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "trivia-question",
		"data":    envData,
	})

	event.Data["envelope"] = string(envJSON)
	h.logger.Debug("trivia answer processed", "correct", correct, "session", event.SessionID)
	return nil
}

func (h *triviaEventHook) handleScores(event plugin.Event) error {
	h.logger.Info("trivia leaderboard requested", "session", event.SessionID)

	entries := h.scores.GetLeaderboard()

	envData := map[string]any{
		"entries": entries,
	}
	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "trivia-leaderboard",
		"data":    envData,
	})

	event.Data["envelope"] = string(envJSON)
	return nil
}
