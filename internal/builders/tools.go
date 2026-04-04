package builders

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Session tracks the in-progress state of a builder flow for one caller.
type Session struct {
	BuilderName string
	Inputs      map[string]string
	CurrentStep string // name of the last completed step (empty if none)
}

// SessionManager tracks active builder sessions keyed by an opaque session ID
// (e.g. the chat session ID). It is safe for concurrent use.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// Get returns the session for the given key, or nil if none exists.
func (sm *SessionManager) Get(key string) *Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[key]
}

// Set stores a session for the given key.
func (sm *SessionManager) Set(key string, s *Session) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[key] = s
}

// Delete removes a session for the given key.
func (sm *SessionManager) Delete(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, key)
}

// StartBuilderResult is returned by HandleStartBuilder.
type StartBuilderResult struct {
	Builder     string `json:"builder"`
	Description string `json:"description"`
	TotalSteps  int    `json:"total_steps"`
	FirstStep   string `json:"first_step"`
	Prompt      string `json:"prompt"`
	Required    bool   `json:"required"`
}

// StepResult is returned by HandleBuilderStep.
type StepResult struct {
	Builder   string       `json:"builder"`
	Step      string       `json:"step"`
	Status    string       `json:"status"` // "next", "complete", "error"
	NextStep  string       `json:"next_step,omitempty"`
	Prompt    string       `json:"prompt,omitempty"`
	Required  bool         `json:"required,omitempty"`
	Result    *BuildResult `json:"result,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// HandleStartBuilder starts a new builder flow. It returns the first step prompt
// and metadata about the builder.
func HandleStartBuilder(reg *Registry, sm *SessionManager, sessionKey string, input map[string]any) (string, error) {
	builderName, _ := input["builder_name"].(string)
	if builderName == "" {
		// Return a list of available builders.
		names := reg.ListBuilders()
		return fmt.Sprintf("Available builders: %s. Call nanite_start_builder with builder_name set to one of these.", strings.Join(names, ", ")), nil
	}

	b := reg.Get(builderName)
	if b == nil {
		names := reg.ListBuilders()
		return "", fmt.Errorf("unknown builder %q — available builders: %s", builderName, strings.Join(names, ", "))
	}

	first := b.FirstStep()
	if first == nil {
		return "", fmt.Errorf("builder %q has no steps", builderName)
	}

	// Create a new session.
	sm.Set(sessionKey, &Session{
		BuilderName: builderName,
		Inputs:      make(map[string]string),
		CurrentStep: "",
	})

	result := StartBuilderResult{
		Builder:     b.Name,
		Description: b.Description,
		TotalSteps:  len(b.Steps),
		FirstStep:   first.Name,
		Prompt:      first.Prompt,
		Required:    first.Required,
	}

	data, _ := json.Marshal(result)
	return string(data), nil
}

// HandleBuilderStep processes one step of a builder flow. It validates the value,
// stores it, and returns the next step or the final build result.
func HandleBuilderStep(reg *Registry, sm *SessionManager, sessionKey string, input map[string]any) (string, error) {
	builderName, _ := input["builder_name"].(string)
	stepName, _ := input["step_name"].(string)
	value, _ := input["value"].(string)

	if builderName == "" || stepName == "" {
		return "", fmt.Errorf("builder_name and step_name are required")
	}

	b := reg.Get(builderName)
	if b == nil {
		return "", fmt.Errorf("unknown builder %q", builderName)
	}

	sess := sm.Get(sessionKey)
	if sess == nil || sess.BuilderName != builderName {
		return "", fmt.Errorf("no active %q builder session — call nanite_start_builder first", builderName)
	}

	// Validate the step value.
	step := b.StepByName(stepName)
	if step == nil {
		return "", fmt.Errorf("unknown step %q in builder %q", stepName, builderName)
	}

	// Apply default if value is empty and a default exists.
	trimmed := strings.TrimSpace(value)
	if trimmed == "" && step.Default != "" {
		trimmed = step.Default
	}

	if err := b.ValidateStep(stepName, trimmed); err != nil {
		result := StepResult{
			Builder: builderName,
			Step:    stepName,
			Status:  "error",
			Error:   err.Error(),
			Prompt:  step.Prompt,
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	}

	// Store the value.
	sess.Inputs[step.Field] = trimmed
	sess.CurrentStep = stepName
	sm.Set(sessionKey, sess)

	// Check if this was the last step.
	if b.IsLastStep(stepName) {
		// Run the build function.
		buildResult, err := b.BuildFunc(sess.Inputs)
		if err != nil {
			result := StepResult{
				Builder: builderName,
				Step:    stepName,
				Status:  "error",
				Error:   err.Error(),
			}
			data, _ := json.Marshal(result)
			// Clean up session on build failure.
			sm.Delete(sessionKey)
			return string(data), nil
		}

		result := StepResult{
			Builder: builderName,
			Step:    stepName,
			Status:  "complete",
			Result:  buildResult,
		}
		data, _ := json.Marshal(result)
		sm.Delete(sessionKey)
		return string(data), nil
	}

	// Return the next step.
	next := b.NextStep(stepName)
	if next == nil {
		return "", fmt.Errorf("internal error: no next step after %q", stepName)
	}

	result := StepResult{
		Builder:  builderName,
		Step:     stepName,
		Status:   "next",
		NextStep: next.Name,
		Prompt:   next.Prompt,
		Required: next.Required,
	}
	data, _ := json.Marshal(result)
	return string(data), nil
}
