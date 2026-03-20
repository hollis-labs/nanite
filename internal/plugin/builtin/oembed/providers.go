package oembed

import (
	"regexp"
)

// Provider represents a known oEmbed provider with URL matching patterns.
type Provider struct {
	Name        string
	URLPatterns []*regexp.Regexp
	Endpoint    string // oEmbed endpoint template (use {url} as placeholder)
}

// DefaultProviders is the built-in registry of known oEmbed providers.
var DefaultProviders = []*Provider{
	{
		Name: "YouTube",
		URLPatterns: []*regexp.Regexp{
			regexp.MustCompile(`https?://(www\.)?youtube\.com/watch\?`),
			regexp.MustCompile(`https?://youtu\.be/`),
			regexp.MustCompile(`https?://(www\.)?youtube\.com/shorts/`),
		},
		Endpoint: "https://www.youtube.com/oembed?url={url}&format=json",
	},
	{
		Name: "Spotify",
		URLPatterns: []*regexp.Regexp{
			regexp.MustCompile(`https?://open\.spotify\.com/(track|album|playlist|episode|show)/`),
		},
		Endpoint: "https://open.spotify.com/oembed?url={url}",
	},
	{
		Name: "Vimeo",
		URLPatterns: []*regexp.Regexp{
			regexp.MustCompile(`https?://(www\.)?vimeo\.com/\d+`),
		},
		Endpoint: "https://vimeo.com/api/oembed.json?url={url}",
	},
	{
		Name: "SoundCloud",
		URLPatterns: []*regexp.Regexp{
			regexp.MustCompile(`https?://soundcloud\.com/.+/.+`),
		},
		Endpoint: "https://soundcloud.com/oembed?url={url}&format=json",
	},
	{
		Name: "Flickr",
		URLPatterns: []*regexp.Regexp{
			regexp.MustCompile(`https?://(www\.)?flickr\.com/photos/`),
		},
		Endpoint: "https://www.flickr.com/services/oembed/?url={url}&format=json",
	},
}

// MatchProvider checks a URL against all known providers and returns the first match.
func MatchProvider(rawURL string) *Provider {
	for _, provider := range DefaultProviders {
		for _, pattern := range provider.URLPatterns {
			if pattern.MatchString(rawURL) {
				return provider
			}
		}
	}
	return nil
}
