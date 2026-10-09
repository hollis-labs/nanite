package testhome

import (
	"os"
	"path/filepath"
	"testing"

	paths "github.com/hollis-labs/libs/util/apppaths"
	"github.com/hollis-labs/nanite/internal/brand"
)

func TestMain(m *testing.M) { Main(m) }

// TestIsolatedLayoutIsOutsideRealHome is the check lead asked for
// (CW-20260930-0208): with Isolate in place, a materializing resolution of
// nanite's layout, the kind that chmods and creates, lands under the temp
// tree and never under the real home.
func TestIsolatedLayoutIsOutsideRealHome(t *testing.T) {
	layout, err := paths.Resolve(brand.ID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	AssertOutsideRealHome(t, layout.DataDir(), layout.StateDir(), layout.CacheDir(), layout.ConfigDir(), layout.MainDB())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	AssertOutsideRealHome(t, home)
}

func TestUnder(t *testing.T) {
	for _, tc := range []struct {
		parent, child string
		want          bool
	}{
		{"/home/u", "/home/u", true},
		{"/home/u", "/home/u/.local/share/nanite", true},
		{"/home/u", "/home/u2/x", false},
		{"/home/u", "/var/tmp/nanite-test-home-1/home", false},
		{"/home/u", filepath.Join("/home/u", "..", "v"), false},
	} {
		if got := under(tc.parent, tc.child); got != tc.want {
			t.Errorf("under(%q, %q) = %v, want %v", tc.parent, tc.child, got, tc.want)
		}
	}
}
