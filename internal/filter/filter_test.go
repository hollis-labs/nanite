package filter

import (
	"strings"
	"testing"
)

func TestNoEmoji_StripsEmoji(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple smiley",
			input: "Hello \U0001F600 World",
			want:  "Hello  World",
		},
		{
			name:  "multiple emoji",
			input: "\U0001F680 Launch \U0001F525 Fire \U0001F4A5 Boom",
			want:  " Launch  Fire  Boom",
		},
		{
			name:  "no emoji passthrough",
			input: "Plain text with punctuation, numbers 123, and symbols: @#$%",
			want:  "Plain text with punctuation, numbers 123, and symbols: @#$%",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "emoji at boundaries",
			input: "\U0001F44D start middle \U0001F44E end \U0001F389",
			want:  " start middle  end ",
		},
		{
			name:  "flag emoji (regional indicators)",
			input: "Flag: \U0001F1FA\U0001F1F8 here",
			want:  "Flag:  here",
		},
		{
			name:  "weather symbols",
			input: "Weather: \u2600\u2601\u2602 report",
			want:  "Weather:  report",
		},
		{
			name:  "dingbats",
			input: "Check \u2714 done \u2716 fail",
			want:  "Check  done  fail",
		},
		{
			name:  "preserves non-latin text",
			input: "\u4F60\u597D\u4E16\u754C Hello",
			want:  "\u4F60\u597D\u4E16\u754C Hello",
		},
		{
			name:  "preserves math symbols",
			input: "x + y = z, a < b > c",
			want:  "x + y = z, a < b > c",
		},
		{
			name:  "skin tone modifiers stripped",
			input: "Hand: \U0001F44B\U0001F3FD wave",
			want:  "Hand:  wave",
		},
		{
			name:  "zwj sequence stripped",
			input: "Family: \U0001F468\u200D\U0001F469\u200D\U0001F467 here",
			want:  "Family:  here",
		},
		{
			name:  "preserves markdown",
			input: "# Title\n\n- Item 1\n- Item 2\n\n**bold** and _italic_",
			want:  "# Title\n\n- Item 1\n- Item 2\n\n**bold** and _italic_",
		},
		{
			name:  "preserves code blocks",
			input: "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```",
			want:  "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NoEmoji(tt.input)
			if got != tt.want {
				t.Errorf("NoEmoji(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestChain_Apply(t *testing.T) {
	c := NewChain()
	c.Add("upper", strings.ToUpper)
	c.Add("trim", strings.TrimSpace)

	got := c.Apply("  hello  ")
	want := "HELLO"
	if got != want {
		t.Errorf("Chain.Apply = %q, want %q", got, want)
	}
}

func TestChain_Empty(t *testing.T) {
	c := NewChain()
	input := "unchanged text"
	got := c.Apply(input)
	if got != input {
		t.Errorf("empty Chain.Apply should return input unchanged, got %q", got)
	}
}

func TestChain_Names(t *testing.T) {
	c := NewChain()
	c.Add("a", NoEmoji)
	c.Add("b", NoEmoji)

	names := c.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("Names() = %v, want [a b]", names)
	}
}

func TestChain_Len(t *testing.T) {
	c := NewChain()
	if c.Len() != 0 {
		t.Errorf("empty chain Len() = %d, want 0", c.Len())
	}
	c.Add("x", NoEmoji)
	if c.Len() != 1 {
		t.Errorf("chain Len() = %d, want 1", c.Len())
	}
}

func TestFromNames(t *testing.T) {
	// Known filter
	c := FromNames([]string{"no_emoji"})
	if c.Len() != 1 {
		t.Errorf("FromNames([no_emoji]) Len() = %d, want 1", c.Len())
	}

	// Unknown filter is skipped
	c2 := FromNames([]string{"no_emoji", "nonexistent"})
	if c2.Len() != 1 {
		t.Errorf("FromNames([no_emoji, nonexistent]) Len() = %d, want 1", c2.Len())
	}

	// Empty list
	c3 := FromNames(nil)
	if c3.Len() != 0 {
		t.Errorf("FromNames(nil) Len() = %d, want 0", c3.Len())
	}
}

func TestFromNames_IntegrationWithNoEmoji(t *testing.T) {
	c := FromNames([]string{"no_emoji"})
	got := c.Apply("Hello \U0001F600 World")
	want := "Hello  World"
	if got != want {
		t.Errorf("FromNames integration: got %q, want %q", got, want)
	}
}

func TestRegistry_ContainsNoEmoji(t *testing.T) {
	if _, ok := Registry["no_emoji"]; !ok {
		t.Error("Registry should contain no_emoji filter")
	}
}
