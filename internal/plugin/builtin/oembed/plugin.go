package oembed

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	hostplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/fragments-engine/plugin"
)

func init() {
	hostplugin.RegisterPlugin("oembed", func() plugin.Plugin { return New() })
}

// OEmbedPlugin provides automatic rich previews for links shared in chat.
// When a message contains a URL from a known oEmbed provider (YouTube, Spotify,
// etc.), the plugin fetches the oEmbed metadata and returns an oembed-card envelope.
type OEmbedPlugin struct {
	host   plugin.Host
	status plugin.PluginStatus
}

// New creates a new OEmbedPlugin instance.
func New() *OEmbedPlugin {
	return &OEmbedPlugin{}
}

func (p *OEmbedPlugin) ID() string          { return "oembed" }
func (p *OEmbedPlugin) Name() string        { return "oEmbed Rich Previews" }
func (p *OEmbedPlugin) Version() string     { return "0.1.0" }
func (p *OEmbedPlugin) Description() string { return "Automatic rich previews for YouTube, Spotify, and other oEmbed links" }
func (p *OEmbedPlugin) Dependencies() []string { return nil }

func (p *OEmbedPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Register event hook for message.sent — scans for URLs.
	hook := &oembedEventHook{
		logger: logger,
	}
	if err := host.RegisterEventHook([]string{"message.sent"}, hook); err != nil {
		return fmt.Errorf("failed to register event hook: %w", err)
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("oembed plugin loaded", "version", p.Version(), "providers", len(DefaultProviders))
	return nil
}

func (p *OEmbedPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("oembed plugin unloaded")
	}
	return nil
}

func (p *OEmbedPlugin) Status() plugin.PluginStatus {
	return p.status
}

// urlRegex matches http(s):// URLs in message text.
var urlRegex = regexp.MustCompile(`https?://[^\s<>"]+`)

// oembedEventHook scans messages for URLs and generates rich previews.
type oembedEventHook struct {
	logger plugin.Logger
}

func (h *oembedEventHook) EventTypes() []string {
	return []string{"message.sent"}
}

func (h *oembedEventHook) Handle(ctx context.Context, event plugin.Event) error {
	content, _ := event.Data["content"].(string)
	if content == "" {
		return nil
	}

	// Skip messages that are plugin commands (start with !).
	if strings.HasPrefix(strings.TrimSpace(content), "!") {
		return nil
	}

	// Find all URLs in the message.
	urls := urlRegex.FindAllString(content, 5) // max 5 URLs per message
	if len(urls) == 0 {
		return nil
	}

	// Try to match the first URL against a known provider.
	for _, rawURL := range urls {
		provider := MatchProvider(rawURL)
		if provider == nil {
			continue
		}

		h.logger.Info("oembed URL detected", "url", rawURL, "provider", provider.Name, "session", event.SessionID)

		result, err := FetchOEmbed(ctx, provider, rawURL)
		if err != nil {
			h.logger.Warn("oembed fetch failed", "url", rawURL, "error", fmt.Sprintf("%v", err))
			continue
		}
		if result == nil {
			continue
		}

		envData := map[string]any{
			"title":          result.Title,
			"description":    result.Description,
			"thumbnail_url":  result.ThumbnailURL,
			"provider_name":  result.ProviderName,
			"provider_url":   result.ProviderURL,
			"type":           result.Type,
			"url":            result.URL,
			"author_name":    result.AuthorName,
			"html":           result.HTML,
		}
		envJSON, _ := json.Marshal(map[string]any{
			"kind":    "envelope",
			"version": 1,
			"type":    "oembed-card",
			"data":    envData,
		})

		event.Data["envelope"] = string(envJSON)
		h.logger.Debug("oembed-card envelope attached", "provider", provider.Name)
		return nil // Only process the first matching URL.
	}

	return nil
}
