package marvel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/plugin"
)

func init() {
	hostplugin.RegisterPlugin("marvel", func() plugin.Plugin { return New() })
}

// MarvelPlugin provides Marvel character and TMDB movie search via chat commands.
// Users trigger it with "!marvel <name>" for character search or "!movie <title>"
// for TMDB movie search. Returns marvel-character or marvel-movie envelopes.
type MarvelPlugin struct {
	host       plugin.Host
	marvelPub  string
	marvelPriv string
	tmdbKey    string
	status     plugin.PluginStatus
}

// New creates a new MarvelPlugin instance.
func New() *MarvelPlugin {
	return &MarvelPlugin{}
}

func (p *MarvelPlugin) ID() string          { return "marvel" }
func (p *MarvelPlugin) Name() string        { return "Marvel + TMDB Mashup" }
func (p *MarvelPlugin) Version() string     { return "0.1.0" }
func (p *MarvelPlugin) Description() string { return "Marvel character + TMDB movie search — !marvel and !movie commands" }
func (p *MarvelPlugin) Dependencies() []string { return nil }

func (p *MarvelPlugin) Load(host plugin.Host) error {
	p.host = host
	logger := host.Logger()

	// Resolve API keys from config (env var -> config file -> empty for demo mode).
	marvelPub, err := host.GetConfig("marvel_public_key")
	if err != nil {
		marvelPub = os.Getenv("MARVEL_PUBLIC_KEY")
	}
	marvelPriv, err := host.GetConfig("marvel_private_key")
	if err != nil {
		marvelPriv = os.Getenv("MARVEL_PRIVATE_KEY")
	}
	tmdbKey, err := host.GetConfig("tmdb_api_key")
	if err != nil {
		tmdbKey = os.Getenv("TMDB_API_KEY")
	}

	p.marvelPub = marvelPub
	p.marvelPriv = marvelPriv
	p.tmdbKey = tmdbKey

	if marvelPub == "" || marvelPriv == "" {
		logger.Warn("MARVEL_PUBLIC_KEY / MARVEL_PRIVATE_KEY not set — Marvel search in demo mode")
	}
	if tmdbKey == "" {
		logger.Warn("TMDB_API_KEY not set — movie search in demo mode")
	}

	// Register event hook for message.sent — intercepts !marvel and !movie commands.
	hook := &marvelEventHook{
		logger:     logger,
		marvelPub:  marvelPub,
		marvelPriv: marvelPriv,
		tmdbKey:    tmdbKey,
	}
	if err := host.RegisterEventHook([]string{"message.sent"}, hook); err != nil {
		return fmt.Errorf("failed to register event hook: %w", err)
	}

	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	logger.Info("marvel plugin loaded", "version", p.Version(),
		"marvel_demo", marvelPub == "",
		"tmdb_demo", tmdbKey == "")
	return nil
}

func (p *MarvelPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("marvel plugin unloaded")
	}
	return nil
}

func (p *MarvelPlugin) Status() plugin.PluginStatus {
	return p.status
}

// marvelEventHook intercepts messages starting with "!marvel " or "!movie ".
type marvelEventHook struct {
	logger     plugin.Logger
	marvelPub  string
	marvelPriv string
	tmdbKey    string
}

func (h *marvelEventHook) EventTypes() []string {
	return []string{"message.sent"}
}

func (h *marvelEventHook) Handle(ctx context.Context, event plugin.Event) error {
	content, _ := event.Data["content"].(string)
	if content == "" {
		return nil
	}

	trimmed := strings.TrimSpace(content)

	if strings.HasPrefix(trimmed, "!marvel ") {
		query := strings.TrimSpace(strings.TrimPrefix(trimmed, "!marvel"))
		if query == "" {
			return nil
		}
		return h.handleMarvelSearch(ctx, event, query)
	}

	if strings.HasPrefix(trimmed, "!movie ") {
		query := strings.TrimSpace(strings.TrimPrefix(trimmed, "!movie"))
		if query == "" {
			return nil
		}
		return h.handleMovieSearch(ctx, event, query)
	}

	return nil
}

func (h *marvelEventHook) handleMarvelSearch(ctx context.Context, event plugin.Event, query string) error {
	h.logger.Info("marvel character search triggered", "query", query, "session", event.SessionID)

	result, err := SearchCharacter(ctx, h.marvelPub, h.marvelPriv, query)
	if err != nil {
		h.logger.Error("marvel search failed", "error", fmt.Sprintf("%v", err))
		return nil
	}
	if result == nil {
		h.logger.Info("marvel search returned no results", "query", query)
		return nil
	}

	envData := map[string]any{
		"name":        result.Name,
		"description": result.Description,
		"image_url":   result.ImageURL,
		"comic_count": result.ComicCount,
		"series_count": result.SeriesCount,
		"movies":      result.Movies,
		"source":      result.Source,
	}
	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "marvel-character",
		"data":    envData,
	})

	event.Data["envelope"] = string(envJSON)
	h.logger.Debug("marvel-character envelope attached", "query", query)
	return nil
}

func (h *marvelEventHook) handleMovieSearch(ctx context.Context, event plugin.Event, query string) error {
	h.logger.Info("movie search triggered", "query", query, "session", event.SessionID)

	result, err := SearchMovie(ctx, h.tmdbKey, query)
	if err != nil {
		h.logger.Error("movie search failed", "error", fmt.Sprintf("%v", err))
		return nil
	}
	if result == nil {
		h.logger.Info("movie search returned no results", "query", query)
		return nil
	}

	envData := map[string]any{
		"title":        result.Title,
		"overview":     result.Overview,
		"poster_url":   result.PosterURL,
		"release_date": result.ReleaseDate,
		"vote_average": result.VoteAverage,
		"vote_count":   result.VoteCount,
		"source":       result.Source,
	}
	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "marvel-movie",
		"data":    envData,
	})

	event.Data["envelope"] = string(envJSON)
	h.logger.Debug("marvel-movie envelope attached", "query", query)
	return nil
}
