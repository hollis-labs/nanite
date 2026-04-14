package plugin

import "testing"

func TestCheckShadcnCompat(t *testing.T) {
	tests := []struct {
		name, plugin, host string
		wantErr            bool
	}{
		{"empty skips", "", "1.0.0", false},
		{"exact match", "1.0.0", "1.0.0", false},
		{"exact mismatch", "1.0.1", "1.0.0", true},
		{"caret same major ok", "^1.0.0", "1.5.2", false},
		{"caret same major at floor", "^1.2.0", "1.2.0", false},
		{"caret below floor", "^1.2.0", "1.1.0", true},
		{"caret major mismatch", "^2.0.0", "1.5.2", true},
		{"tilde same major+minor ok", "~1.2.0", "1.2.7", false},
		{"tilde minor mismatch", "~1.2.0", "1.3.0", true},
		{"malformed range", "1.2", "1.0.0", true},
		{"unsupported prefix", ">=1.0.0", "1.0.0", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkShadcnCompatAgainst(tc.plugin, tc.host)
			if (err != nil) != tc.wantErr {
				t.Fatalf("plugin=%q host=%q wantErr=%v err=%v", tc.plugin, tc.host, tc.wantErr, err)
			}
		})
	}
}
