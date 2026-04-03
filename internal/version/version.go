package version

// Version is the current release version. Updated manually on release.
var Version = "0.3.0-beta"

// GitSHA is the git commit hash, injected at build time via:
//
//	go build -ldflags "-X github.com/hollis-labs/conduit/internal/version.GitSHA=$(git rev-parse --short HEAD)"
var GitSHA = "dev"

// Full returns "version (sha)" for display.
func Full() string {
	return Version + " (" + GitSHA + ")"
}
