package api

import "github.com/hollis-labs/nanite/internal/store"

// ConsumerView is a consumer as the API returns it. Field order is the store
// row's.
type ConsumerView struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func consumerToView(c *store.Consumer) ConsumerView {
	return ConsumerView{ID: c.ID, Slug: c.Slug, Name: c.Name, CreatedAt: c.CreatedAt}
}

// consumersToView keeps nil as nil and an empty list as [].
func consumersToView(cs []store.Consumer) []ConsumerView {
	if cs == nil {
		return nil
	}
	out := make([]ConsumerView, len(cs))
	for i := range cs {
		out[i] = consumerToView(&cs[i])
	}
	return out
}

// DocumentView is a session context document as the API returns it. Field
// order is the store row's.
type DocumentView struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	Content     string `json:"content"`
	SizeBytes   int    `json:"size_bytes"`
	Included    bool   `json:"included"`
	FullContent bool   `json:"full_content"`
	Summary     string `json:"summary"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func documentToView(d *store.Document) DocumentView {
	return DocumentView{
		ID:          d.ID,
		SessionID:   d.SessionID,
		Name:        d.Name,
		MimeType:    d.MimeType,
		Content:     d.Content,
		SizeBytes:   d.SizeBytes,
		Included:    d.Included,
		FullContent: d.FullContent,
		Summary:     d.Summary,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

// documentsToView keeps nil as nil and an empty list as [].
func documentsToView(docs []store.Document) []DocumentView {
	if docs == nil {
		return nil
	}
	out := make([]DocumentView, len(docs))
	for i := range docs {
		out[i] = documentToView(&docs[i])
	}
	return out
}
