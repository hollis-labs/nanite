package trivia

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// TriviaQuestion holds a single trivia question for the envelope.
type TriviaQuestion struct {
	Question     string   `json:"question"`
	Category     string   `json:"category"`
	Difficulty   string   `json:"difficulty"`
	Answers      []string `json:"answers"`
	CorrectIndex int      `json:"correct_index"`
	Source       string   `json:"source"`
}

// openTDBResponse mirrors the Open Trivia Database JSON response.
type openTDBResponse struct {
	ResponseCode int `json:"response_code"`
	Results      []struct {
		Category         string   `json:"category"`
		Type             string   `json:"type"`
		Difficulty       string   `json:"difficulty"`
		Question         string   `json:"question"`
		CorrectAnswer    string   `json:"correct_answer"`
		IncorrectAnswers []string `json:"incorrect_answers"`
	} `json:"results"`
}

// demoQuestions provides hardcoded questions when the API is unavailable.
var demoQuestions = []*TriviaQuestion{
	{
		Question:     "What is the chemical symbol for gold?",
		Category:     "Science",
		Difficulty:   "easy",
		Answers:      []string{"Au", "Ag", "Fe", "Cu"},
		CorrectIndex: 0,
		Source:       "Trivia (demo mode)",
	},
	{
		Question:     "In what year did the Titanic sink?",
		Category:     "History",
		Difficulty:   "easy",
		Answers:      []string{"1905", "1912", "1920", "1898"},
		CorrectIndex: 1,
		Source:       "Trivia (demo mode)",
	},
	{
		Question:     "Which planet is known as the Red Planet?",
		Category:     "Science",
		Difficulty:   "easy",
		Answers:      []string{"Venus", "Jupiter", "Mars", "Saturn"},
		CorrectIndex: 2,
		Source:       "Trivia (demo mode)",
	},
	{
		Question:     "What programming language was created by Guido van Rossum?",
		Category:     "Technology",
		Difficulty:   "medium",
		Answers:      []string{"Java", "C++", "Ruby", "Python"},
		CorrectIndex: 3,
		Source:       "Trivia (demo mode)",
	},
	{
		Question:     "What is the largest ocean on Earth?",
		Category:     "Geography",
		Difficulty:   "easy",
		Answers:      []string{"Pacific Ocean", "Atlantic Ocean", "Indian Ocean", "Arctic Ocean"},
		CorrectIndex: 0,
		Source:       "Trivia (demo mode)",
	},
	{
		Question:     "Who painted the Mona Lisa?",
		Category:     "Art",
		Difficulty:   "easy",
		Answers:      []string{"Michelangelo", "Leonardo da Vinci", "Raphael", "Donatello"},
		CorrectIndex: 1,
		Source:       "Trivia (demo mode)",
	},
	{
		Question:     "What does 'HTTP' stand for?",
		Category:     "Technology",
		Difficulty:   "medium",
		Answers:      []string{"HyperText Transfer Protocol", "High Tech Transfer Protocol", "HyperText Transmission Process", "High Transfer Text Protocol"},
		CorrectIndex: 0,
		Source:       "Trivia (demo mode)",
	},
	{
		Question:     "Which element has the atomic number 1?",
		Category:     "Science",
		Difficulty:   "easy",
		Answers:      []string{"Helium", "Oxygen", "Hydrogen", "Carbon"},
		CorrectIndex: 2,
		Source:       "Trivia (demo mode)",
	},
}

// GetDemoQuestion returns a random demo question.
func GetDemoQuestion() *TriviaQuestion {
	idx := rand.Intn(len(demoQuestions))
	// Return a copy to avoid mutation.
	q := *demoQuestions[idx]
	answers := make([]string, len(q.Answers))
	copy(answers, q.Answers)
	q.Answers = answers
	return &q
}

// FetchQuestion fetches a trivia question from the Open Trivia Database API.
func FetchQuestion(ctx context.Context) (*TriviaQuestion, error) {
	reqURL := "https://opentdb.com/api.php?amount=1&type=multiple"

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenTDB API error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var otdb openTDBResponse
	if err := json.Unmarshal(body, &otdb); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if otdb.ResponseCode != 0 || len(otdb.Results) == 0 {
		return nil, fmt.Errorf("no results from OpenTDB (code: %d)", otdb.ResponseCode)
	}

	raw := otdb.Results[0]

	// Decode HTML entities in all text fields.
	question := html.UnescapeString(raw.Question)
	category := html.UnescapeString(raw.Category)
	correct := html.UnescapeString(raw.CorrectAnswer)

	incorrect := make([]string, len(raw.IncorrectAnswers))
	for i, a := range raw.IncorrectAnswers {
		incorrect[i] = html.UnescapeString(a)
	}

	// Shuffle the correct answer into the options.
	answers := make([]string, 0, 4)
	answers = append(answers, incorrect...)

	// Insert the correct answer at a random position.
	correctIdx := rand.Intn(len(answers) + 1)
	answers = append(answers, "")
	copy(answers[correctIdx+1:], answers[correctIdx:])
	answers[correctIdx] = correct

	return &TriviaQuestion{
		Question:     question,
		Category:     category,
		Difficulty:   raw.Difficulty,
		Answers:      answers,
		CorrectIndex: correctIdx,
		Source:       "Open Trivia DB",
	}, nil
}
