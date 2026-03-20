package marvel

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CharacterResult holds Marvel character data for the envelope.
type CharacterResult struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	ImageURL    string   `json:"image_url"`
	ComicCount  int      `json:"comic_count"`
	SeriesCount int      `json:"series_count"`
	Movies      []string `json:"movies"`
	Source      string   `json:"source"`
}

// marvelAPIResponse mirrors the relevant subset of Marvel's character search JSON.
type marvelAPIResponse struct {
	Data struct {
		Results []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Thumbnail   struct {
				Path      string `json:"path"`
				Extension string `json:"extension"`
			} `json:"thumbnail"`
			Comics struct {
				Available int `json:"available"`
			} `json:"comics"`
			Series struct {
				Available int `json:"available"`
			} `json:"series"`
		} `json:"results"`
	} `json:"data"`
}

// demoCharacters provides hardcoded responses for demo mode.
var demoCharacters = map[string]*CharacterResult{
	"iron man": {
		Name:        "Iron Man",
		Description: "Wounded, captured and forced to build a weapon by his enemies, billionaire industrialist Tony Stark instead created an advanced suit of armor to save his life and escape captivity. Now with a new outlook on life, Tony uses his money and intelligence to make the world a safer, better place as Iron Man.",
		ImageURL:    "https://i.annihil.us/u/prod/marvel/i/mg/9/c0/527bb7b37ff55/portrait_uncanny.jpg",
		ComicCount:  2639,
		SeriesCount: 649,
		Movies:      []string{"Iron Man (2008)", "Iron Man 2 (2010)", "The Avengers (2012)", "Iron Man 3 (2013)", "Avengers: Age of Ultron (2015)", "Avengers: Endgame (2019)"},
		Source:      "Marvel (demo mode)",
	},
	"spider-man": {
		Name:        "Spider-Man (Peter Parker)",
		Description: "Bitten by a radioactive spider, high school student Peter Parker gained the speed, strength and powers of a spider. Adopting the name Spider-Man, Peter hoped to start a career using his new abilities. Taught that with great power comes great responsibility, Spider-Man has vowed to use his powers to help people.",
		ImageURL:    "https://i.annihil.us/u/prod/marvel/i/mg/3/50/526548a343e4b/portrait_uncanny.jpg",
		ComicCount:  4164,
		SeriesCount: 1002,
		Movies:      []string{"Spider-Man (2002)", "Spider-Man: Homecoming (2017)", "Spider-Man: Far From Home (2019)", "Spider-Man: No Way Home (2021)"},
		Source:      "Marvel (demo mode)",
	},
	"thor": {
		Name:        "Thor",
		Description: "As the Norse God of thunder and lightning, Thor wields one of the greatest weapons ever made, the enchanted hammer Mjolnir. While others have described Thor as an pointlessly powerful being, he has quite a complex personality.",
		ImageURL:    "https://i.annihil.us/u/prod/marvel/i/mg/d/d0/5269657a74350/portrait_uncanny.jpg",
		ComicCount:  2169,
		SeriesCount: 504,
		Movies:      []string{"Thor (2011)", "The Avengers (2012)", "Thor: The Dark World (2013)", "Thor: Ragnarok (2017)", "Avengers: Endgame (2019)", "Thor: Love and Thunder (2022)"},
		Source:      "Marvel (demo mode)",
	},
}

// SearchCharacter searches for a Marvel character by name.
// Falls back to demo data when API keys are not configured.
func SearchCharacter(ctx context.Context, publicKey, privateKey, name string) (*CharacterResult, error) {
	if name == "" {
		return nil, fmt.Errorf("character name is required")
	}

	if publicKey == "" || privateKey == "" {
		return searchCharacterDemo(name), nil
	}

	return searchCharacterLive(ctx, publicKey, privateKey, name)
}

func searchCharacterDemo(name string) *CharacterResult {
	lower := strings.ToLower(name)
	for key, result := range demoCharacters {
		if strings.Contains(lower, key) {
			return result
		}
	}

	// Fallback for unknown characters in demo mode.
	return &CharacterResult{
		Name:        name,
		Description: fmt.Sprintf("Demo mode: no cached data for %q. Set MARVEL_PUBLIC_KEY and MARVEL_PRIVATE_KEY for live search.", name),
		ImageURL:    "https://i.annihil.us/u/prod/marvel/i/mg/b/40/image_not_available/portrait_uncanny.jpg",
		ComicCount:  0,
		SeriesCount: 0,
		Movies:      nil,
		Source:      "Marvel (demo mode)",
	}
}

func searchCharacterLive(ctx context.Context, publicKey, privateKey, name string) (*CharacterResult, error) {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	hash := fmt.Sprintf("%x", md5.Sum([]byte(ts+privateKey+publicKey)))

	reqURL := fmt.Sprintf("https://gateway.marvel.com/v1/public/characters?name=%s&apikey=%s&ts=%s&hash=%s",
		url.QueryEscape(name), publicKey, ts, hash)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Marvel API error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var apiResp marvelAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(apiResp.Data.Results) == 0 {
		return nil, nil
	}

	char := apiResp.Data.Results[0]
	imageURL := char.Thumbnail.Path + "/portrait_uncanny." + char.Thumbnail.Extension

	return &CharacterResult{
		Name:        char.Name,
		Description: char.Description,
		ImageURL:    imageURL,
		ComicCount:  char.Comics.Available,
		SeriesCount: char.Series.Available,
		Movies:      nil, // Live movie data comes from TMDB
		Source:      "Marvel API",
	}, nil
}
