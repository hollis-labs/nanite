package mcp

import "testing"

func TestParseHeaderJSON(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    map[string]string
		wantErr bool
	}{
		{name: "empty", in: "", want: nil},
		{name: "empty object", in: "{}", want: nil},
		{name: "whitespace", in: "   ", want: nil},
		{
			name: "a bearer token",
			in:   `{"Authorization":"Bearer abc.def"}`,
			want: map[string]string{"Authorization": "Bearer abc.def"},
		},
		{
			name: "blank key is dropped",
			in:   `{"":"x","X-Real":"y"}`,
			want: map[string]string{"X-Real": "y"},
		},
		// Go's http client would refuse these too, but refusing at registration
		// beats refusing on every call — and a header value is exactly where a
		// smuggled newline would matter.
		{name: "newline in value", in: `{"X":"a\r\nInjected: 1"}`, wantErr: true},
		{name: "newline in key", in: "{\"X\\nY\":\"a\"}", wantErr: true},
		{name: "not an object", in: `["Authorization"]`, wantErr: true},
		{name: "malformed", in: `{oops`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseHeaderJSON(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
