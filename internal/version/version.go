package version

// Version is the product version. Release and local builds inject it from the
// nearest repo-level v* tag via ldflags (see the Makefile VERSION_LDFLAGS):
//
//	go build -ldflags "-X github.com/hollis-labs/nanite/internal/version.Version=$(git describe --tags --match 'v*' --dirty | sed 's/^v//')"
//
// A plain `go build` reports "dev". pkg/pluginapi/vN tags are the plugin API
// module's own contract version and are not the product version.
var Version = "dev"

// GitSHA is the git commit hash, injected at build time via:
//
//	go build -ldflags "-X github.com/hollis-labs/nanite/internal/version.GitSHA=$(git rev-parse --short HEAD)"
var GitSHA = "dev"

// Full returns "version (sha)" for display.
func Full() string {
	return Version + " (" + GitSHA + ")"
}
