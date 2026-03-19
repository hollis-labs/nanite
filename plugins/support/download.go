package support

import (
	"html/template"
	"net/http"
	"strings"
)

// DownloadHandler serves a formatted HTML ticket page for print/PDF download.
type DownloadHandler struct {
	store *TicketStore
}

// NewDownloadHandler creates a new download handler backed by the given store.
func NewDownloadHandler(store *TicketStore) *DownloadHandler {
	return &DownloadHandler{store: store}
}

func (h *DownloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ticketID := r.URL.Query().Get("ticket_id")
	if ticketID == "" {
		http.Error(w, `{"error":"missing ticket_id parameter"}`, http.StatusBadRequest)
		return
	}

	ticket := h.store.Get(ticketID)
	if ticket == nil {
		http.Error(w, `{"error":"ticket not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// No Content-Disposition — opens in browser tab instead of downloading

	data := map[string]interface{}{
		"Ticket":     ticket,
		"KBArticles": strings.Join(ticket.KBArticles, ", "),
	}

	if err := ticketTemplate.Execute(w, data); err != nil {
		http.Error(w, "failed to render template", http.StatusInternalServerError)
	}
}

var ticketTemplate = template.Must(template.New("ticket").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Ticket.ID}} — {{.Ticket.Title}}</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    color: #1e293b;
    background: #f8fafc;
    line-height: 1.5;
  }

  /* Top bar */
  .topbar {
    background: #0f172a;
    color: #fff;
    padding: 12px 32px;
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .topbar-brand { font-weight: 700; font-size: 0.9rem; letter-spacing: 0.03em; }
  .topbar-brand span { color: #60a5fa; }
  .topbar-meta { font-size: 0.75rem; color: #94a3b8; }

  /* Ticket header */
  .ticket-header {
    background: #fff;
    border-bottom: 1px solid #e2e8f0;
    padding: 24px 32px;
  }
  .ticket-id-row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 8px;
  }
  .ticket-id {
    font-family: "SF Mono", "Fira Code", monospace;
    font-size: 0.8rem;
    font-weight: 600;
    color: #3b82f6;
    background: #eff6ff;
    border: 1px solid #bfdbfe;
    padding: 2px 10px;
    border-radius: 4px;
  }
  .badge {
    display: inline-block;
    padding: 2px 10px;
    border-radius: 12px;
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.03em;
  }
  .badge-open { background: #dbeafe; color: #1d4ed8; }
  .badge-in_progress { background: #e0e7ff; color: #4338ca; }
  .badge-resolved { background: #dcfce7; color: #15803d; }
  .badge-closed { background: #f1f5f9; color: #64748b; }
  .badge-low { background: #f0fdf4; color: #166534; }
  .badge-medium { background: #fefce8; color: #854d0e; }
  .badge-high { background: #fff7ed; color: #c2410c; }
  .badge-critical { background: #fef2f2; color: #b91c1c; }
  .ticket-title { font-size: 1.35rem; font-weight: 600; color: #0f172a; }

  /* Content area */
  .content { max-width: 800px; margin: 0 auto; padding: 24px 32px 40px; }

  /* Field grid */
  .field-grid {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 1px;
    background: #e2e8f0;
    border: 1px solid #e2e8f0;
    border-radius: 8px;
    overflow: hidden;
    margin-bottom: 24px;
  }
  .field-cell {
    background: #fff;
    padding: 12px 16px;
  }
  .field-label {
    font-size: 0.65rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: #94a3b8;
    margin-bottom: 4px;
  }
  .field-value { font-size: 0.9rem; color: #1e293b; }

  /* Sections */
  .section-card {
    background: #fff;
    border: 1px solid #e2e8f0;
    border-radius: 8px;
    padding: 16px 20px;
    margin-bottom: 16px;
  }
  .section-label {
    font-size: 0.7rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: #64748b;
    margin-bottom: 8px;
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .section-label::before {
    content: "";
    display: block;
    width: 3px;
    height: 14px;
    border-radius: 2px;
    background: #3b82f6;
  }
  .section-body {
    font-size: 0.9rem;
    color: #334155;
    white-space: pre-wrap;
  }

  /* Routing */
  .routing {
    background: #eff6ff;
    border: 1px solid #bfdbfe;
    border-radius: 8px;
    padding: 16px 20px;
    margin-bottom: 16px;
  }
  .routing .section-label::before { background: #2563eb; }
  .routing .section-body {
    font-weight: 600;
    color: #1d4ed8;
    font-size: 0.95rem;
  }

  /* Timeline */
  .timeline {
    display: flex;
    gap: 24px;
    font-size: 0.75rem;
    color: #94a3b8;
    padding: 12px 0;
    border-top: 1px solid #e2e8f0;
    margin-top: 8px;
  }
  .timeline strong { color: #64748b; font-weight: 500; }

  /* Disclaimer */
  .disclaimer {
    margin-top: 24px;
    border: 1px dashed #f59e0b;
    border-radius: 8px;
    padding: 14px 18px;
    background: #fffbeb;
    display: flex;
    gap: 10px;
    align-items: flex-start;
  }
  .disclaimer-icon {
    width: 20px;
    height: 20px;
    background: #f59e0b;
    color: #fff;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.7rem;
    font-weight: 700;
    flex-shrink: 0;
    margin-top: 1px;
  }
  .disclaimer-text { font-size: 0.8rem; color: #92400e; line-height: 1.5; }
  .disclaimer-text strong { color: #78350f; }

  @media print {
    body { background: #fff; }
    .topbar { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
    .badge, .ticket-id, .routing, .field-grid { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
  }
</style>
</head>
<body>
  <!-- Top Bar -->
  <div class="topbar">
    <div class="topbar-brand"><span>Fragments</span> Engine — IT Service Management</div>
    <div class="topbar-meta">Ticket Record</div>
  </div>

  <!-- Ticket Header -->
  <div class="ticket-header">
    <div class="ticket-id-row">
      <span class="ticket-id">{{.Ticket.ID}}</span>
      <span class="badge badge-{{.Ticket.Status}}">{{.Ticket.Status}}</span>
      <span class="badge badge-{{.Ticket.Priority}}">{{.Ticket.Priority}} priority</span>
    </div>
    <div class="ticket-title">{{.Ticket.Title}}</div>
  </div>

  <!-- Content -->
  <div class="content">
    <!-- Field Grid -->
    <div class="field-grid">
      <div class="field-cell">
        <div class="field-label">Category</div>
        <div class="field-value">{{.Ticket.Category}}</div>
      </div>
      <div class="field-cell">
        <div class="field-label">Requester</div>
        <div class="field-value">{{if .Ticket.Requester}}{{.Ticket.Requester}}{{else}}—{{end}}</div>
      </div>
      <div class="field-cell">
        <div class="field-label">Assigned To</div>
        <div class="field-value">{{if .Ticket.AssignedTo}}{{.Ticket.AssignedTo}}{{else}}Unassigned{{end}}</div>
      </div>
    </div>

    <!-- Description -->
    <div class="section-card">
      <div class="section-label">Description</div>
      <div class="section-body">{{.Ticket.Description}}</div>
    </div>

    {{if .Ticket.StepsTried}}
    <div class="section-card">
      <div class="section-label">Steps Already Tried</div>
      <div class="section-body">{{.Ticket.StepsTried}}</div>
    </div>
    {{end}}

    {{if .Ticket.Resolution}}
    <div class="section-card">
      <div class="section-label">Resolution</div>
      <div class="section-body">{{.Ticket.Resolution}}</div>
    </div>
    {{end}}

    {{if .KBArticles}}
    <div class="section-card">
      <div class="section-label">Related KB Articles</div>
      <div class="section-body">{{.KBArticles}}</div>
    </div>
    {{end}}

    <!-- Routing -->
    <div class="routing">
      <div class="section-label">Routing Recommendation</div>
      <div class="section-body">{{.Ticket.Routing}}</div>
    </div>

    <!-- Timeline -->
    <div class="timeline">
      <div><strong>Created:</strong> {{.Ticket.CreatedAt.Format "Jan 2, 2006 3:04 PM MST"}}</div>
      <div><strong>Updated:</strong> {{.Ticket.UpdatedAt.Format "Jan 2, 2006 3:04 PM MST"}}</div>
    </div>

    <!-- Disclaimer -->
    <div class="disclaimer">
      <div class="disclaimer-icon">!</div>
      <div class="disclaimer-text">
        <strong>Proof of Concept</strong> — This is a demonstration of the Fragments Engine IT Service Management platform.
        In production, this ticket would be created via API integration with BMC Helix ITSM, automatically classified,
        and routed to the appropriate support team.
      </div>
    </div>
  </div>
</body>
</html>`))
