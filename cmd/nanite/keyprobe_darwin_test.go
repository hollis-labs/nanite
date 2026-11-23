package main

import (
	"context"
	"slices"
	"testing"
)

// The keychain is never touched: runSecurity is replaced. What matters is the argv (no -w or -g,
// so the password is never read and no access prompt can appear) and the exit-code mapping.
func TestKeychainHasReadsAttributesOnly(t *testing.T) {
	prev := runSecurity
	t.Cleanup(func() { runSecurity = prev })
	var calls [][]string
	code := 0
	runSecurity = func(_ context.Context, args ...string) ([]byte, int, error) {
		calls = append(calls, args)
		return []byte("attributes"), code, nil
	}
	for _, c := range []struct {
		code           int
		found, wantErr bool
	}{{0, true, false}, {44, false, false}, {51, false, true}} {
		code = c.code
		found, checked, err := keychainHas(t.Context(), "hollis-labs", "nanite/provider/anthropic")
		if found != c.found || !checked || (err != nil) != c.wantErr {
			t.Errorf("exit %d: found=%v checked=%v err=%v", c.code, found, checked, err)
		}
	}
	for _, args := range calls {
		if !slices.Equal(args, []string{"find-generic-password", "-s", "hollis-labs", "-a", "nanite/provider/anthropic"}) ||
			slices.Contains(args, "-w") || slices.Contains(args, "-g") {
			t.Fatalf("security %v", args)
		}
	}
}
