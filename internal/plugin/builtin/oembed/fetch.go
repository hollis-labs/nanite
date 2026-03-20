package oembed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OEmbedResult holds the fetched oEmbed metadata for the envelope.
type OEmbedResult struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	ThumbnailURL string `json:"thumbnail_url"`
	ProviderName string `json:"provider_name"`
	ProviderURL  string `json:"provider_url"`
	Type         string `json:"type"`         // "video", "photo", "rich", "link"
	URL          string `json:"url"`           // The original URL
	AuthorName   string `json:"author_name"`
	HTML         string `json:"html"`          // Embed HTML (for video/rich types)
}

// oembedRawResponse mirrors the standard oEmbed JSON response.
type oembedRawResponse struct {
	Type            string `json:"type"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	AuthorName      string `json:"author_name"`
	AuthorURL       string `json:"author_url"`
	ProviderName    string `json:"provider_name"`
	ProviderURL     string `json:"provider_url"`
	ThumbnailURL    string `json:"thumbnail_url"`
	ThumbnailWidth  int    `json:"thumbnail_width"`
	ThumbnailHeight int    `json:"thumbnail_height"`
	HTML            string `json:"html"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	URL             string `json:"url"` // For photo type, the actual image URL
}

// FetchOEmbed fetches oEmbed metadata from a provider's endpoint.
func FetchOEmbed(ctx context.Context, provider *Provider, rawURL string) (*OEmbedResult, error) {
	endpoint := strings.Replace(provider.Endpoint, "{url}", url.QueryEscape(rawURL), 1)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oEmbed fetch error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oEmbed returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var raw oembedRawResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	thumbnailURL := raw.ThumbnailURL
	// For photo type, the main URL is the image itself.
	if raw.Type == "photo" && raw.URL != "" && thumbnailURL == "" {
		thumbnailURL = raw.URL
	}

	return &OEmbedResult{
		Title:        raw.Title,
		Description:  raw.Description,
		ThumbnailURL: thumbnailURL,
		ProviderName: raw.ProviderName,
		ProviderURL:  raw.ProviderURL,
		Type:         raw.Type,
		URL:          rawURL,
		AuthorName:   raw.AuthorName,
		HTML:         raw.HTML,
	}, nil
}
