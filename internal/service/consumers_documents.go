package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// ConsumerStore is the store surface ConsumerService uses.
type ConsumerStore interface {
	ListConsumers(ctx context.Context) ([]store.Consumer, error)
	GetConsumer(ctx context.Context, id string) (*store.Consumer, error)
	CreateConsumer(ctx context.Context, c *store.Consumer) error
	UpdateConsumer(ctx context.Context, c *store.Consumer) error
	DeleteConsumer(ctx context.Context, id string) error
}

// ConsumerService is the transport-facing home for consumer rows (the
// parties agents are built for). It is a pass-through; a delete that other
// rows still reference fails in the store.
type ConsumerService struct {
	store ConsumerStore
}

func NewConsumerService(st ConsumerStore) *ConsumerService { return &ConsumerService{store: st} }

// List returns every consumer.
func (s *ConsumerService) List(ctx context.Context) ([]store.Consumer, error) {
	return s.store.ListConsumers(ctx)
}

// Get returns a consumer, or nil when there is none.
func (s *ConsumerService) Get(ctx context.Context, id string) (*store.Consumer, error) {
	return s.store.GetConsumer(ctx, id)
}

// Create inserts a consumer; the store fills in its ID and timestamp.
func (s *ConsumerService) Create(ctx context.Context, c *store.Consumer) error {
	return s.store.CreateConsumer(ctx, c)
}

// Update writes every field of c.
func (s *ConsumerService) Update(ctx context.Context, c *store.Consumer) error {
	return s.store.UpdateConsumer(ctx, c)
}

// Delete removes a consumer.
func (s *ConsumerService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteConsumer(ctx, id)
}

// DocumentStore is the store surface DocumentService uses.
type DocumentStore interface {
	ListDocuments(ctx context.Context, sessionID string) ([]store.Document, error)
	CreateDocument(ctx context.Context, d *store.Document) error
	GetDocument(ctx context.Context, id string) (*store.Document, error)
	UpdateDocumentToggles(ctx context.Context, id string, included, fullContent bool, summary string) error
	DeleteDocument(ctx context.Context, id string) error
	GetSessionContextPrompt(ctx context.Context, sessionID string) (string, error)
	SetSessionContextPrompt(ctx context.Context, sessionID, prompt string) error
}

// DocumentService owns a session's context documents and its context prompt.
// Store errors come back unwrapped.
type DocumentService struct {
	store DocumentStore
}

func NewDocumentService(st DocumentStore) *DocumentService { return &DocumentService{store: st} }

// DocumentPatch is an update to a document's context settings. A nil field
// keeps the stored value.
type DocumentPatch struct {
	Included    *bool
	FullContent *bool
	Summary     *string
}

// List returns a session's documents.
func (s *DocumentService) List(ctx context.Context, sessionID string) ([]store.Document, error) {
	return s.store.ListDocuments(ctx, sessionID)
}

// Create inserts a document; the store fills in its ID, size and timestamps.
func (s *DocumentService) Create(ctx context.Context, d *store.Document) error {
	return s.store.CreateDocument(ctx, d)
}

// Get returns a document. Any read failure is an error.
func (s *DocumentService) Get(ctx context.Context, id string) (*store.Document, error) {
	return s.store.GetDocument(ctx, id)
}

// UpdateToggles applies patch to doc's included, full-content and summary
// settings, keeping the stored value of each one the patch leaves nil, and
// writes them. doc is updated in place only once the write succeeds.
func (s *DocumentService) UpdateToggles(ctx context.Context, doc *store.Document, patch DocumentPatch) error {
	included := doc.Included
	fullContent := doc.FullContent
	summary := doc.Summary
	if patch.Included != nil {
		included = *patch.Included
	}
	if patch.FullContent != nil {
		fullContent = *patch.FullContent
	}
	if patch.Summary != nil {
		summary = *patch.Summary
	}
	if err := s.store.UpdateDocumentToggles(ctx, doc.ID, included, fullContent, summary); err != nil {
		return err
	}
	doc.Included = included
	doc.FullContent = fullContent
	doc.Summary = summary
	return nil
}

// Delete removes a document.
func (s *DocumentService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteDocument(ctx, id)
}

// ContextPrompt returns the session's context prompt.
func (s *DocumentService) ContextPrompt(ctx context.Context, sessionID string) (string, error) {
	return s.store.GetSessionContextPrompt(ctx, sessionID)
}

// SetContextPrompt stores the session's context prompt.
func (s *DocumentService) SetContextPrompt(ctx context.Context, sessionID, prompt string) error {
	return s.store.SetSessionContextPrompt(ctx, sessionID, prompt)
}
