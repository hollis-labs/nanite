package chat

import (
	"context"
	"testing"
)

func TestParseDeltaMode(t *testing.T) {
	tests := []struct {
		in      string
		want    DeltaMode
		wantErr bool
	}{
		{"", DeltaModePhased, false},
		{"phased", DeltaModePhased, false},
		{"live", DeltaModeLive, false},
		{"  LIVE ", DeltaModeLive, false},
		{"Phased", DeltaModePhased, false},
		{"lvie", DeltaModePhased, true},
		{"stream", DeltaModePhased, true},
	}
	for _, tc := range tests {
		got, err := ParseDeltaMode(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseDeltaMode(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("ParseDeltaMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDeltaModeContextRoundTrip(t *testing.T) {
	if got := DeltaModeFromContext(context.Background()); got != DeltaModePhased {
		t.Errorf("bare ctx = %q, want %q", got, DeltaModePhased)
	}
	//nolint:staticcheck // intentional nil ctx
	if got := DeltaModeFromContext(nil); got != DeltaModePhased {
		t.Errorf("nil ctx = %q, want %q", got, DeltaModePhased)
	}
	ctx := WithDeltaMode(context.Background(), DeltaModeLive)
	if got := DeltaModeFromContext(ctx); got != DeltaModeLive {
		t.Errorf("live ctx = %q, want %q", got, DeltaModeLive)
	}
	if got := DeltaModeFromContext(WithDeltaMode(ctx, DeltaModePhased)); got != DeltaModePhased {
		t.Errorf("overridden ctx = %q, want %q", got, DeltaModePhased)
	}
}
