package api

import "github.com/hollis-labs/nanite/internal/store"

// PinView is a pinned-content item as the API returns it. Field order is
// the store row's; session_id and project_id drop out when unset.
type PinView struct {
	ID        string  `json:"id"`
	SessionID *string `json:"session_id,omitempty"`
	Scope     string  `json:"scope"`
	ProjectID string  `json:"project_id,omitempty"`
	Content   string  `json:"content"`
	AgentID   string  `json:"agent_id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

func pinToView(p *store.PinnedContent) PinView {
	return PinView{
		ID:        p.ID,
		SessionID: p.SessionID,
		Scope:     p.Scope,
		ProjectID: p.ProjectID,
		Content:   p.Content,
		AgentID:   p.AgentID,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

// pinsToView keeps nil as nil and an empty list as [].
func pinsToView(pins []store.PinnedContent) []PinView {
	if pins == nil {
		return nil
	}
	out := make([]PinView, len(pins))
	for i := range pins {
		out[i] = pinToView(&pins[i])
	}
	return out
}

// ReminderView is a reminder as the API returns it. Field order is the store
// row's; project_id and fired_at drop out when unset.
type ReminderView struct {
	ID          string  `json:"id"`
	SessionID   string  `json:"session_id"`
	Scope       string  `json:"scope"`
	ProjectID   string  `json:"project_id,omitempty"`
	Text        string  `json:"text"`
	TriggerJSON string  `json:"trigger_json"`
	FiredAt     *string `json:"fired_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func reminderToView(r *store.Reminder) ReminderView {
	return ReminderView{
		ID:          r.ID,
		SessionID:   r.SessionID,
		Scope:       r.Scope,
		ProjectID:   r.ProjectID,
		Text:        r.Text,
		TriggerJSON: r.TriggerJSON,
		FiredAt:     r.FiredAt,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// remindersToView keeps nil as nil and an empty list as [].
func remindersToView(rems []store.Reminder) []ReminderView {
	if rems == nil {
		return nil
	}
	out := make([]ReminderView, len(rems))
	for i := range rems {
		out[i] = reminderToView(&rems[i])
	}
	return out
}
