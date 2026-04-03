package context

// TokenEstimator estimates the token count for a string. Implementations
// may use a simple heuristic or a real tokenizer (tiktoken, etc.).
type TokenEstimator interface {
	Estimate(text string) int
}

// DefaultEstimator uses a chars/4 heuristic. Good enough for budget
// allocation where precision isn't critical.
type DefaultEstimator struct{}

func (DefaultEstimator) Estimate(text string) int {
	n := len(text) / 4
	if n == 0 && len(text) > 0 {
		n = 1
	}
	return n
}
