package giphy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/go-plugin"
)

func init() {
	hostplugin.RegisterPlugin("giphy", func() plugin.Plugin { return New() })
}

// GiphyPlugin provides inline GIF search via the Giphy API.
// Users trigger it with "!giphy <query>" in chat; the plugin's event hook
// intercepts the message and returns a giphy-card envelope.
type GiphyPlugin struct {
	host   plugin.Host
	apiKey string
	status plugin.PluginStatus
}

// New creates a new GiphyPlugin instance.
func New() *GiphyPlugin {
	return &GiphyPlugin{}
}

func (p *GiphyPlugin) ID() string          { return "giphy" }
func (p *GiphyPlugin) Name() string        { return "Giphy GIF Search" }
func (p *GiphyPlugin) Version() string     { return "0.1.0" }
func (p *GiphyPlugin) Description() string { return "Giphy GIF search — !giphy <query> for inline GIFs" }
func (p *GiphyPlugin) Dependencies() []string { return nil }

func (p *GiphyPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Register config schema so the API key and rating are configurable via the UI.
	if err := host.RegisterConfigSchema([]plugin.ConfigFieldDef{
		{
			Key:         "giphy_api_key",
			Type:        "secret",
			Label:       "Giphy API Key",
			Description: "Get a free key from developers.giphy.com. Leave blank for demo mode.",
		},
		{
			Key:         "giphy_rating",
			Type:        "select",
			Label:       "Content Rating",
			Description: "Maximum content rating for search results",
			Default:     "g",
			Options:     []string{"g", "pg", "pg-13", "r"},
		},
	}); err != nil {
		logger.Warn("failed to register config schema", "error", fmt.Sprintf("%v", err))
	}

	// Resolve API key: keychain (via GetConfig) → env var → demo mode.
	apiKey, err := host.GetConfig("giphy_api_key")
	if err != nil || apiKey == "" {
		apiKey = os.Getenv("GIPHY_API_KEY")
	}
	p.apiKey = apiKey

	if apiKey == "" {
		logger.Warn("GIPHY_API_KEY not set — running in demo mode with built-in GIFs")
	}

	// Register event hook for message.sent — intercepts !giphy commands.
	hook := &giphyEventHook{
		logger: logger,
		apiKey: apiKey,
	}
	if err := host.RegisterEventHook([]string{"message.sent"}, hook); err != nil {
		return fmt.Errorf("failed to register event hook: %w", err)
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("giphy plugin loaded", "version", p.Version(), "demo_mode", apiKey == "")
	return nil
}

func (p *GiphyPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("giphy plugin unloaded")
	}
	return nil
}

func (p *GiphyPlugin) Status() plugin.PluginStatus {
	return p.status
}

// giphyEventHook intercepts messages starting with "!giphy " and returns
// a giphy-card envelope with the search result.
type giphyEventHook struct {
	logger plugin.Logger
	apiKey string
}

func (h *giphyEventHook) EventTypes() []string {
	return []string{"message.sent"}
}

func (h *giphyEventHook) Handle(ctx context.Context, event plugin.Event) error {
	content, _ := event.Data["content"].(string)
	if content == "" {
		return nil
	}

	// Only respond to !giphy commands.
	if !strings.HasPrefix(strings.TrimSpace(content), "!giphy ") {
		return nil
	}

	query := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "!giphy"))
	if query == "" {
		return nil
	}

	h.logger.Info("giphy search triggered", "query", query, "session", event.SessionID)

	result, err := Search(ctx, h.apiKey, query)
	if err != nil {
		h.logger.Error("giphy search failed", "error", fmt.Sprintf("%v", err))
		return nil // non-fatal — don't break the message flow
	}
	if result == nil {
		h.logger.Info("giphy search returned no results", "query", query)
		return nil
	}

	// Build envelope JSON and attach it to the event data for the chat engine
	// to extract and render.
	envData := map[string]any{
		"title":   result.Title,
		"gif_url": result.GifURL,
		"source":  result.Source,
		"query":   result.Query,
	}
	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "giphy-card",
		"data":    envData,
	})

	// Store the envelope in event data for downstream processing.
	event.Data["envelope"] = string(envJSON)

	h.logger.Debug("giphy envelope attached", "query", query)
	return nil
}
