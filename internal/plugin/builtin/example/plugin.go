// Package example is a reference plugin that demonstrates every Conduit plugin
// feature. Use it as a starting point for new plugins.
//
// Features demonstrated:
//   - Config schema registration (frontend renders a settings form)
//   - DB-backed config read/write via Host.GetConfig / Host.SetConfig
//   - Event hooks (post-hook: message.sent, session.start)
//   - CRUD handler (auto-wired to /api/plugins/notes/*)
//   - UI component registration (envelope type)
//   - Connector registration (outbound webhook)
//   - Install/Uninstall lifecycle (Installable/Uninstallable interfaces)
//
// To enable this plugin, add a blank import in allplugins.go:
//
//	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/example"
//
// Then create plugins/example/plugin.yaml (or rely on the compiled-in init).
package example

import (
	"context"
	"fmt"
	"sync"
	"time"

	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("example", func() plugin.Plugin { return New() })
}

// ---------------------------------------------------------------------------
// Plugin struct
// ---------------------------------------------------------------------------

// ExamplePlugin is a reference implementation demonstrating all plugin features.
type ExamplePlugin struct {
	host   plugin.Host
	status plugin.PluginStatus
}

func New() *ExamplePlugin { return &ExamplePlugin{} }

func (p *ExamplePlugin) ID() string             { return "example" }
func (p *ExamplePlugin) Name() string           { return "Example Plugin" }
func (p *ExamplePlugin) Version() string        { return "0.1.0" }
func (p *ExamplePlugin) Description() string    { return "Reference plugin demonstrating all Conduit plugin features" }
func (p *ExamplePlugin) Dependencies() []string { return nil }

// ---------------------------------------------------------------------------
// Load — the main entry point. Register everything here.
// ---------------------------------------------------------------------------

func (p *ExamplePlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// 1. Config schema — defines what appears in the plugin settings UI.
	//    Field types: "string", "bool", "int", "select", "secret"
	if err := host.RegisterConfigSchema([]plugin.ConfigFieldDef{
		{
			Key:         "greeting",
			Type:        "string",
			Label:       "Greeting Message",
			Description: "Message shown when a session starts",
			Default:     "Hello from Example Plugin!",
		},
		{
			Key:     "verbose",
			Type:    "bool",
			Label:   "Verbose Logging",
			Default: false,
		},
		{
			Key:         "webhook_url",
			Type:        "secret",
			Label:       "Webhook URL",
			Description: "URL to POST event notifications to",
		},
		{
			Key:     "log_level",
			Type:    "select",
			Label:   "Log Level",
			Options: []string{"debug", "info", "warn", "error"},
			Default: "info",
		},
	}); err != nil {
		logger.Warn("failed to register config schema", "error", err)
	}

	// 2. Read a config value — resolution: env var → DB → config file → default.
	greeting, _ := host.GetConfig("greeting")
	logger.Info("config loaded", "greeting", greeting)

	// 3. Event hooks — react to system events.
	hook := &exampleHook{plugin: p}
	if err := host.RegisterEventHook(hook.EventTypes(), hook); err != nil {
		return fmt.Errorf("register event hook: %w", err)
	}

	// 4. CRUD handler — auto-wires REST endpoints at /api/plugins/notes/*.
	notesHandler := &NotesHandler{notes: make(map[string]*Note)}
	if err := host.RegisterCRUDHandler("notes", notesHandler); err != nil {
		return fmt.Errorf("register notes CRUD: %w", err)
	}

	// 5. UI component — registers an envelope type for the frontend.
	if err := host.RegisterUIComponent(plugin.UIComponent{
		ID:          "example-card",
		Type:        plugin.UIComponentTypeEnvelope,
		Name:        "Example Card",
		Description: "A simple card envelope for demonstration",
		Props: map[string]interface{}{
			"envelope_type": "example-card",
		},
	}); err != nil {
		return fmt.Errorf("register UI component: %w", err)
	}

	// 6. Connector — register an outbound integration.
	connector := &ExampleConnector{logger: logger}
	if err := host.RegisterConnector("example-webhook", connector); err != nil {
		logger.Warn("failed to register connector", "error", err)
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	logger.Info("example plugin loaded", "version", p.Version())
	return nil
}

func (p *ExamplePlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("example plugin unloaded")
	}
	return nil
}

func (p *ExamplePlugin) Status() plugin.PluginStatus { return p.status }

// Install runs first-time setup. Implement plugin.Installable to use this.
func (p *ExamplePlugin) Install(host plugin.Host) error {
	host.Logger().Info("example plugin installed — first-time setup complete")
	return nil
}

// Uninstall cleans up on removal. Implement plugin.Uninstallable to use this.
func (p *ExamplePlugin) Uninstall(host plugin.Host) error {
	host.Logger().Info("example plugin uninstalled — cleanup complete")
	return nil
}

// ---------------------------------------------------------------------------
// Event Hook
// ---------------------------------------------------------------------------

type exampleHook struct {
	plugin *ExamplePlugin
}

func (h *exampleHook) EventTypes() []string {
	return []string{"message.sent", "session.start", "config.changed"}
}

func (h *exampleHook) Handle(ctx context.Context, event plugin.Event) error {
	logger := h.plugin.host.Logger()

	switch event.Type {
	case "session.start":
		greeting, _ := h.plugin.host.GetConfig("greeting")
		logger.Info("session started", "session", event.SessionID, "greeting", greeting)

	case "message.sent":
		logger.Debug("message sent in session", "session", event.SessionID)

	case "config.changed":
		logger.Info("config changed", "key", event.Data["key"], "plugin", event.Data["plugin_id"])
	}
	return nil
}

// ---------------------------------------------------------------------------
// CRUD Handler — Notes
// ---------------------------------------------------------------------------

// Note is a simple data model for the CRUD example.
type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// NotesHandler implements plugin.CRUDHandler with an in-memory store.
// A real plugin would use the SQLite DB via host.GetService("store").
type NotesHandler struct {
	mu    sync.RWMutex
	notes map[string]*Note
}

func (h *NotesHandler) Create(ctx context.Context, resource interface{}) (interface{}, error) {
	data, ok := resource.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid note data")
	}
	note := &Note{
		ID:        fmt.Sprintf("note-%d", time.Now().UnixNano()),
		Title:     fmt.Sprintf("%v", data["title"]),
		Content:   fmt.Sprintf("%v", data["content"]),
		CreatedAt: time.Now(),
	}
	h.mu.Lock()
	h.notes[note.ID] = note
	h.mu.Unlock()
	return note, nil
}

func (h *NotesHandler) Read(ctx context.Context, id string) (interface{}, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	note, ok := h.notes[id]
	if !ok {
		return nil, fmt.Errorf("note %s not found", id)
	}
	return note, nil
}

func (h *NotesHandler) Update(ctx context.Context, id string, resource interface{}) (interface{}, error) {
	data, ok := resource.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid note data")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	note, exists := h.notes[id]
	if !exists {
		return nil, fmt.Errorf("note %s not found", id)
	}
	if title, ok := data["title"]; ok {
		note.Title = fmt.Sprintf("%v", title)
	}
	if content, ok := data["content"]; ok {
		note.Content = fmt.Sprintf("%v", content)
	}
	return note, nil
}

func (h *NotesHandler) Delete(ctx context.Context, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.notes[id]; !exists {
		return fmt.Errorf("note %s not found", id)
	}
	delete(h.notes, id)
	return nil
}

func (h *NotesHandler) List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]interface{}, 0, len(h.notes))
	for _, note := range h.notes {
		result = append(result, note)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Connector — outbound webhook
// ---------------------------------------------------------------------------

// ExampleConnector demonstrates the Connector interface for outbound integrations.
type ExampleConnector struct {
	logger plugin.Logger
}

func (c *ExampleConnector) Name() string { return "example-webhook" }

func (c *ExampleConnector) Send(ctx context.Context, payload map[string]interface{}) error {
	// In a real connector, you'd POST to a webhook URL here.
	c.logger.Info("connector: would send payload", "keys", len(payload))
	return nil
}
