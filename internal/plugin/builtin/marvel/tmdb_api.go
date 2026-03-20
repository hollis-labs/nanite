package marvel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// MovieResult holds TMDB movie data for the envelope.
type MovieResult struct {
	Title       string  `json:"title"`
	Overview    string  `json:"overview"`
	PosterURL   string  `json:"poster_url"`
	ReleaseDate string  `json:"release_date"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
	Source      string  `json:"source"`
}

// tmdbSearchResponse mirrors the relevant subset of TMDB's /3/search/movie JSON.
type tmdbSearchResponse struct {
	Results []struct {
		Title       string  `json:"title"`
		Overview    string  `json:"overview"`
		PosterPath  string  `json:"poster_path"`
		ReleaseDate string  `json:"release_date"`
		VoteAverage float64 `json:"vote_average"`
		VoteCount   int     `json:"vote_count"`
	} `json:"results"`
}

// demoMovies provides hardcoded responses for demo mode.
var demoMovies = map[string]*MovieResult{
	"iron man": {
		Title:       "Iron Man",
		Overview:    "After being held captive in an Afghan cave, billionaire engineer Tony Stark creates a unique weaponized suit of armor to fight evil.",
		PosterURL:   "https://image.tmdb.org/t/p/w500/78lPtwv72eTNqFW9COBYI0dWDJa.jpg",
		ReleaseDate: "2008-04-30",
		VoteAverage: 7.6,
		VoteCount:   25000,
		Source:      "TMDB (demo mode)",
	},
	"spider-man": {
		Title:       "Spider-Man: No Way Home",
		Overview:    "Peter Parker is unmasked and no longer able to separate his normal life from the high-stakes of being a super-hero. When he asks for help from Doctor Strange the stakes become even more dangerous.",
		PosterURL:   "https://image.tmdb.org/t/p/w500/1g0dhYtq4irTY1GPXvft6k4YLjm.jpg",
		ReleaseDate: "2021-12-15",
		VoteAverage: 8.0,
		VoteCount:   18000,
		Source:      "TMDB (demo mode)",
	},
	"thor": {
		Title:       "Thor: Ragnarok",
		Overview:    "Thor is imprisoned on the other side of the universe and finds himself in a race against time to get back to Asgard to stop Ragnarok, the prophecy of destruction to his homeland and the end of Asgardian civilization.",
		PosterURL:   "https://image.tmdb.org/t/p/w500/rzRwTcFvttcN1ZpX2xv4j3tSdJu.jpg",
		ReleaseDate: "2017-10-25",
		VoteAverage: 7.6,
		VoteCount:   20000,
		Source:      "TMDB (demo mode)",
	},
}

// SearchMovie searches TMDB for a movie by title.
// Falls back to demo data when API key is not configured.
func SearchMovie(ctx context.Context, apiKey, title string) (*MovieResult, error) {
	if title == "" {
		return nil, fmt.Errorf("movie title is required")
	}

	if apiKey == "" {
		return searchMovieDemo(title), nil
	}

	return searchMovieLive(ctx, apiKey, title)
}

func searchMovieDemo(title string) *MovieResult {
	lower := title
	for i := range lower {
		if lower[i] >= 'A' && lower[i] <= 'Z' {
			lower = lower[:i] + string(rune(lower[i]+32)) + lower[i+1:]
		}
	}

	for key, result := range demoMovies {
		if len(lower) >= len(key) && lower[:len(key)] == key || len(key) >= len(lower) && key[:len(lower)] == lower {
			return result
		}
		// Also check contains
		for i := 0; i <= len(lower)-len(key); i++ {
			if lower[i:i+len(key)] == key {
				return result
			}
		}
	}

	return &MovieResult{
		Title:       title,
		Overview:    fmt.Sprintf("Demo mode: no cached data for %q. Set TMDB_API_KEY for live search.", title),
		PosterURL:   "",
		ReleaseDate: "N/A",
		VoteAverage: 0,
		VoteCount:   0,
		Source:      "TMDB (demo mode)",
	}
}

func searchMovieLive(ctx context.Context, apiKey, title string) (*MovieResult, error) {
	reqURL := fmt.Sprintf("https://api.themoviedb.org/3/search/movie?api_key=%s&query=%s",
		apiKey, url.QueryEscape(title))

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TMDB API error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var tmdbResp tmdbSearchResponse
	if err := json.Unmarshal(body, &tmdbResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(tmdbResp.Results) == 0 {
		return nil, nil
	}

	movie := tmdbResp.Results[0]
	posterURL := ""
	if movie.PosterPath != "" {
		posterURL = "https://image.tmdb.org/t/p/w500" + movie.PosterPath
	}

	return &MovieResult{
		Title:       movie.Title,
		Overview:    movie.Overview,
		PosterURL:   posterURL,
		ReleaseDate: movie.ReleaseDate,
		VoteAverage: movie.VoteAverage,
		VoteCount:   movie.VoteCount,
		Source:      "TMDB",
	}, nil
}
