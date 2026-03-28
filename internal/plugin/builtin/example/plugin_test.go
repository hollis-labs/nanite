package example

import (
	"testing"

	"github.com/hollis-labs/fragments-engine/plugin"
)

func TestExamplePlugin_Interface(t *testing.T) {
	p := New()

	if p.ID() != "example" {
		t.Errorf("expected ID 'example', got %q", p.ID())
	}
	if p.Name() != "Example Plugin" {
		t.Errorf("expected Name 'Example Plugin', got %q", p.Name())
	}
	if p.Version() != "0.1.0" {
		t.Errorf("expected Version '0.1.0', got %q", p.Version())
	}

	// Verify it implements Plugin interface.
	var _ plugin.Plugin = p

	// Verify it implements Installable and Uninstallable.
	var _ plugin.Installable = p
	var _ plugin.Uninstallable = p
}

func TestNotesHandler_CRUD(t *testing.T) {
	h := &NotesHandler{notes: make(map[string]*Note)}
	ctx := t.Context()

	// Create
	result, err := h.Create(ctx, map[string]interface{}{
		"title":   "Test Note",
		"content": "Hello world",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	note := result.(*Note)
	if note.Title != "Test Note" {
		t.Errorf("expected title 'Test Note', got %q", note.Title)
	}

	// Read
	readResult, err := h.Read(ctx, note.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	readNote := readResult.(*Note)
	if readNote.ID != note.ID {
		t.Errorf("Read returned wrong note")
	}

	// Update
	updated, err := h.Update(ctx, note.ID, map[string]interface{}{
		"title": "Updated Title",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	updatedNote := updated.(*Note)
	if updatedNote.Title != "Updated Title" {
		t.Errorf("expected updated title, got %q", updatedNote.Title)
	}

	// List
	list, err := h.List(ctx, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 note, got %d", len(list))
	}

	// Delete
	if err := h.Delete(ctx, note.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify deleted
	_, err = h.Read(ctx, note.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestExampleConnector(t *testing.T) {
	c := &ExampleConnector{logger: &noopLogger{}}
	ctx := t.Context()

	err := c.Send(ctx, map[string]interface{}{"test": true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// noopLogger for testing.
type noopLogger struct{}

func (l *noopLogger) Debug(msg string, kv ...interface{}) {}
func (l *noopLogger) Info(msg string, kv ...interface{})  {}
func (l *noopLogger) Warn(msg string, kv ...interface{})  {}
func (l *noopLogger) Error(msg string, kv ...interface{}) {}
func (l *noopLogger) With(kv ...interface{}) plugin.Logger { return l }
