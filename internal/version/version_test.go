package version

import "testing"

func TestFull(t *testing.T) {
	oldV, oldS := Version, GitSHA
	t.Cleanup(func() { Version, GitSHA = oldV, oldS })

	Version, GitSHA = "0.3.0-beta.1", "98d14ab65e99"
	if got, want := Full(), "0.3.0-beta.1 (98d14ab65e99)"; got != want {
		t.Fatalf("Full() = %q, want %q", got, want)
	}
}

func TestDefaultsAreDev(t *testing.T) {
	// A build without ldflags must not claim a release version.
	if Version != "dev" || GitSHA != "dev" {
		t.Skipf("built with ldflags (Version=%q GitSHA=%q)", Version, GitSHA)
	}
}
