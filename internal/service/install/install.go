// This file defines the public Service type and its InstallHome entry point,
// which extracts the embedded framework assets into the user's ~/.nanite
// directory. See scaffold.go for the package-level doc comment.
package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/assets"
)

// Service is the install package's public entry point. All CLI and MCP
// surfaces call into a Service instance.
type Service struct{}

// New constructs a default Service.
func New() *Service {
	return &Service{}
}

// InstallHomeOptions controls InstallHome.
type InstallHomeOptions struct {
	// Target is the directory to extract to. Defaults to ~/.nanite if empty.
	Target string
	// Force overwrites user-modified files during extract.
	Force bool
}

// InstallHome extracts the embedded framework assets into the target
// directory (typically ~/.nanite). Skips user-modified files unless
// Force is set. Returns the extract report from assets.ExtractTo.
func (s *Service) InstallHome(opts InstallHomeOptions) (*assets.ExtractReport, error) {
	target := opts.Target
	if target == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		target = filepath.Join(home, ".nanite")
	}
	return assets.ExtractTo(target, assets.ExtractOptions{Force: opts.Force})
}
