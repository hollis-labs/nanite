package giphy

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

// GiphyResult holds the data extracted from a Giphy API search response.
type GiphyResult struct {
	Title  string `json:"title"`
	GifURL string `json:"gif_url"`
	Source string `json:"source"`
	Query  string `json:"query"`
}

// giphyAPIResponse mirrors the relevant subset of Giphy's /v1/gifs/search JSON.
type giphyAPIResponse struct {
	Data []struct {
		Title  string `json:"title"`
		Images struct {
			Original struct {
				URL string `json:"url"`
			} `json:"original"`
			FixedWidth struct {
				URL string `json:"url"`
			} `json:"fixed_width"`
		} `json:"images"`
	} `json:"data"`
}

// demoGifs is a fallback lookup used when no API key is configured.
var demoGifs = map[string]string{
	"celebration": "https://media.giphy.com/media/g9582DNuQppxC/giphy.gif",
	"success":     "https://media.giphy.com/media/a0h7sAqON67nO/giphy.gif",
	"thumbs up":   "https://media.giphy.com/media/111ebonMs90YLu/giphy.gif",
	"mind blown":  "https://media.giphy.com/media/xT0xeJpnrWC3XWblEk/giphy.gif",
	"happy":       "https://media.giphy.com/media/BlVnrxJgTGsUw/giphy.gif",
	"dance":       "https://media.giphy.com/media/l0MYt5jPR6QX5APm0/giphy.gif",
	"cat":         "https://media.giphy.com/media/JIX9t2j0ZTN9S/giphy.gif",
	"dog":         "https://media.giphy.com/media/4Zo41lhzKt6iZ8xff9/giphy.gif",
	"hamster":     "https://media.giphy.com/media/l2JhIUyUs8KDCCf3W/giphy.gif",
	"running":     "https://media.giphy.com/media/11BAxHG7paxJcI/giphy.gif",
	"coding":      "https://media.giphy.com/media/ZVik7pBtu9dNS/giphy.gif",
	"coffee":      "https://media.giphy.com/media/DrJm6F9poo4aA/giphy.gif",
	"rocket":      "https://media.giphy.com/media/mi6DsSSNKDbUY/giphy.gif",
	"fire":        "https://media.giphy.com/media/j3IxJRLNLZz9sXR7ZA/giphy.gif",
}

// Search calls the Giphy API (or returns a demo result if apiKey is empty).
func Search(ctx context.Context, apiKey, query string) (*GiphyResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	if apiKey == "" {
		return searchDemo(query), nil
	}

	return searchLive(ctx, apiKey, query)
}

// searchDemo picks a GIF from the built-in demo set.
func searchDemo(query string) *GiphyResult {
	gifURL := ""
	lowerQ := strings.ToLower(query)
	for keyword, u := range demoGifs {
		if strings.Contains(lowerQ, keyword) {
			gifURL = u
			break
		}
	}
	if gifURL == "" {
		// Deterministic pick based on hash of query.
		keys := make([]string, 0, len(demoGifs))
		for k := range demoGifs {
			keys = append(keys, k)
		}
		h := 0
		for _, c := range query {
			h = h*31 + int(c)
		}
		if h < 0 {
			h = -h
		}
		gifURL = demoGifs[keys[h%len(keys)]]
	}

	return &GiphyResult{
		Title:  fmt.Sprintf("Here's your %s!", query),
		GifURL: gifURL,
		Source: "GIPHY (demo mode)",
		Query:  query,
	}
}

// searchLive hits the Giphy API for a real result.
func searchLive(ctx context.Context, apiKey, query string) (*GiphyResult, error) {
	reqURL := fmt.Sprintf("https://api.giphy.com/v1/gifs/search?api_key=%s&q=%s&limit=1&rating=g",
		apiKey, url.QueryEscape(query))

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Giphy API error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var giphyResp giphyAPIResponse
	if err := json.Unmarshal(body, &giphyResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(giphyResp.Data) == 0 {
		return nil, nil // no results
	}

	gif := giphyResp.Data[0]
	gifURL := gif.Images.Original.URL
	if gifURL == "" {
		gifURL = gif.Images.FixedWidth.URL
	}

	return &GiphyResult{
		Title:  fmt.Sprintf("Here's your %s!", query),
		GifURL: gifURL,
		Source: "GIPHY",
		Query:  query,
	}, nil
}
