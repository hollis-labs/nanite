package chat

import (
	"testing"
)

func TestIsPromissoryPreamble(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name: "c395 real world stall preamble",
			input: "I’ll treat this as an exploratory follow-up: verify Nanite, Torque, and Tether against current source, " +
				"inspect Tesseract for related evidence, then capture the alignment question without deciding the migration. " +
				"I’ll first recall the existing Atlas/library context, then inspect the relevant repository guidance and sources.",
			expected: true,
		},
		{
			name:     "simple I will check",
			input:    "I will check the repository for references to that function.",
			expected: true,
		},
		{
			name:     "let me search",
			input:    "Let me search the codebase to see how this is implemented.",
			expected: true,
		},
		{
			name:     "I am going to investigate",
			input:    "I am going to investigate the database schema.",
			expected: true,
		},
		{
			name:     "first I will",
			input:    "First, I'll inspect the files in internal/service.",
			expected: true,
		},
		{
			name:     "let us begin by checking",
			input:    "Let's begin by checking the git status.",
			expected: true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "clarification question",
			input:    "Which repository would you like me to check?",
			expected: false,
		},
		{
			name:     "code fence deliverable",
			input:    "Let me check this. Here is the code:\n```go\nfunc main() {}\n```",
			expected: false,
		},
		{
			name:     "statement of identity",
			input:    "I am Nanite, an AI assistant.",
			expected: false,
		},
		{
			name:     "explanation of concept",
			input:    "Quicksort is a divide-and-conquer algorithm that selects a pivot and partitions the array around it.",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsPromissoryPreamble(tc.input)
			if got != tc.expected {
				t.Errorf("IsPromissoryPreamble(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}
