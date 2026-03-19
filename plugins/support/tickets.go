package support

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"
)

// Ticket represents an IT support ticket.
type Ticket struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Category    string    `json:"category"`               // "network", "access", "vpn", "jira", "general"
	Priority    string    `json:"priority"`                // "low", "medium", "high", "critical"
	Status      string    `json:"status"`                  // "open", "in_progress", "resolved", "closed"
	Requester   string    `json:"requester"`
	AssignedTo  string    `json:"assigned_to,omitempty"`
	Resolution  string    `json:"resolution,omitempty"`
	Routing     string    `json:"routing,omitempty"`       // rough routing suggestion
	StepsTried  string    `json:"steps_tried,omitempty"`
	KBArticles  []string  `json:"kb_articles,omitempty"`   // related KB article IDs
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TicketStore persists tickets to SQLite via Conduit's DB.
type TicketStore struct {
	db *sql.DB
}

// NewTicketStore creates a ticket store. If db is nil, falls back to in-memory.
func NewTicketStore() *TicketStore {
	return &TicketStore{}
}

// SetDB sets the SQLite database and creates the table if needed.
func (s *TicketStore) SetDB(db *sql.DB) {
	s.db = db
	if db != nil {
		db.Exec(`CREATE TABLE IF NOT EXISTS support_tickets (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`)
	}
}

func (s *TicketStore) put(t *Ticket) error {
	if s.db == nil {
		return fmt.Errorf("no database")
	}
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO support_tickets (id, data, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET data = excluded.data`,
		t.ID, string(data), t.CreatedAt.Format(time.RFC3339),
	)
	return err
}

func (s *TicketStore) get(id string) (*Ticket, error) {
	if s.db == nil {
		return nil, fmt.Errorf("ticket %q not found", id)
	}
	var data string
	err := s.db.QueryRow(`SELECT data FROM support_tickets WHERE id = ?`, id).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("ticket %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	var t Ticket
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *TicketStore) list(filters map[string]interface{}) ([]*Ticket, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT data FROM support_tickets ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	filterStatus, _ := filters["status"].(string)
	filterCategory, _ := filters["category"].(string)
	filterRequester, _ := filters["requester"].(string)

	var results []*Ticket
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			continue
		}
		var t Ticket
		if err := json.Unmarshal([]byte(data), &t); err != nil {
			continue
		}
		if filterStatus != "" && t.Status != filterStatus {
			continue
		}
		if filterCategory != "" && t.Category != filterCategory {
			continue
		}
		if filterRequester != "" && t.Requester != filterRequester {
			continue
		}
		results = append(results, &t)
	}
	return results, rows.Err()
}

func (s *TicketStore) del(id string) error {
	if s.db == nil {
		return fmt.Errorf("ticket %q not found", id)
	}
	res, err := s.db.Exec(`DELETE FROM support_tickets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("ticket %q not found", id)
	}
	return nil
}

// Get retrieves a ticket by ID. Returns nil if not found.
func (s *TicketStore) Get(id string) *Ticket {
	t, err := s.get(id)
	if err != nil {
		return nil
	}
	return t
}

// TicketHandler implements plugin.CRUDHandler for tickets.
type TicketHandler struct {
	store *TicketStore
}

// NewTicketHandler creates a new TicketHandler backed by the given store.
func NewTicketHandler(store *TicketStore) *TicketHandler {
	return &TicketHandler{store: store}
}

// Create creates a new ticket from the JSON body.
func (h *TicketHandler) Create(ctx context.Context, resource interface{}) (interface{}, error) {
	raw, err := json.Marshal(resource)
	if err != nil {
		return nil, fmt.Errorf("invalid ticket data: %w", err)
	}
	var t Ticket
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("invalid ticket data: %w", err)
	}

	now := time.Now()
	t.ID = fmt.Sprintf("TKT-%d-%04d", now.UnixMilli(), rand.Intn(10000))

	if t.Status == "" {
		t.Status = "open"
	}
	if t.Priority == "" {
		t.Priority = "medium"
	}

	t.CreatedAt = now
	t.UpdatedAt = now
	t.Routing = routingForCategory(t.Category)

	if err := h.store.put(&t); err != nil {
		return nil, fmt.Errorf("save ticket: %w", err)
	}

	return &t, nil
}

// Read retrieves a ticket by ID.
func (h *TicketHandler) Read(ctx context.Context, id string) (interface{}, error) {
	t, err := h.store.get(id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// Update merges fields into an existing ticket.
func (h *TicketHandler) Update(ctx context.Context, id string, resource interface{}) (interface{}, error) {
	t, err := h.store.get(id)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(resource)
	if err != nil {
		return nil, fmt.Errorf("invalid update data: %w", err)
	}
	var patch map[string]interface{}
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, fmt.Errorf("invalid update data: %w", err)
	}

	if v, ok := patch["title"].(string); ok && v != "" {
		t.Title = v
	}
	if v, ok := patch["description"].(string); ok && v != "" {
		t.Description = v
	}
	if v, ok := patch["category"].(string); ok && v != "" {
		t.Category = v
		t.Routing = routingForCategory(v)
	}
	if v, ok := patch["priority"].(string); ok && v != "" {
		t.Priority = v
	}
	if v, ok := patch["status"].(string); ok && v != "" {
		t.Status = v
	}
	if v, ok := patch["requester"].(string); ok && v != "" {
		t.Requester = v
	}
	if v, ok := patch["assigned_to"].(string); ok {
		t.AssignedTo = v
	}
	if v, ok := patch["resolution"].(string); ok {
		t.Resolution = v
	}
	if v, ok := patch["steps_tried"].(string); ok {
		t.StepsTried = v
	}

	t.UpdatedAt = time.Now()

	if err := h.store.put(t); err != nil {
		return nil, fmt.Errorf("save ticket: %w", err)
	}

	return t, nil
}

// Delete removes a ticket by ID.
func (h *TicketHandler) Delete(ctx context.Context, id string) error {
	return h.store.del(id)
}

// List returns tickets matching optional filters.
func (h *TicketHandler) List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error) {
	tickets, err := h.store.list(filters)
	if err != nil {
		return nil, err
	}
	results := make([]interface{}, len(tickets))
	for i, t := range tickets {
		results[i] = t
	}
	return results, nil
}

// routingForCategory returns a rough routing suggestion based on ticket category.
func routingForCategory(category string) string {
	switch category {
	case "network":
		return "Network Operations — L2 Support"
	case "access":
		return "Identity & Access Management"
	case "vpn":
		return "Network Operations — VPN/Remote Access"
	case "jira":
		return "DevOps Tools — Atlassian Administration"
	case "confluence":
		return "DevOps Tools — Atlassian Administration"
	case "sso", "authentication":
		return "Identity & Access Management — SSO/Federation"
	case "aws", "cloud":
		return "Cloud Infrastructure — AWS Support"
	case "tableau":
		return "Data & Analytics — Tableau Administration"
	case "snowflake":
		return "Data & Analytics — Snowflake Administration"
	case "email", "distribution-list":
		return "Messaging & Collaboration — Exchange/M365"
	case "software":
		return "IT Service Desk — Software Provisioning"
	case "hardware":
		return "IT Service Desk — Hardware/Endpoint"
	case "general":
		return "IT Service Desk — General Intake"
	default:
		return "IT Service Desk — Triage"
	}
}
