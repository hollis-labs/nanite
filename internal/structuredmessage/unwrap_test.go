package structuredmessage

import "testing"

func TestUnwrapText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		text string
		ok   bool
	}{
		{name: "structured", in: `{"v":1,"text":"hello there"}`, text: "hello there", ok: true},
		{name: "structured with whitespace", in: " \n\t{\"v\":1,\"text\":\"  padded text  \"}\n", text: "  padded text  ", ok: true},
		{name: "empty structured text", in: `{"v":1,"text":""}`, text: "", ok: true},
		{name: "legacy json no version", in: `{"text":"no version"}`, ok: false},
		{name: "invalid version zero", in: `{"v":0,"text":"nope"}`, ok: false},
		{name: "invalid version negative", in: `{"v":-1,"text":"nope"}`, ok: false},
		{name: "invalid json", in: `{not json`, ok: false},
		{name: "plain text", in: `plain text`, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := UnwrapText(tc.in)
			if ok != tc.ok {
				t.Fatalf("UnwrapText(%q) ok=%v want %v", tc.in, ok, tc.ok)
			}
			if got != tc.text {
				t.Fatalf("UnwrapText(%q) text=%q want %q", tc.in, got, tc.text)
			}
		})
	}
}
