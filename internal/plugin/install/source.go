package install

import (
	"context"
	"os"
)

// CatalogArchiveSource implements Source by downloading a signed archive
// from a catalog entry over HTTP. Shared by cmd/nanite's CLI catalog
// install flow (plugin_install_flow.go's installFromCatalog) and
// internal/api's GUI/API catalog install handler (catalog.go's
// handleCatalogInstall) — a second hand-rolled copy of this type is exactly
// the kind of divergence AD-04 exists to close (see BuildOptions' doc
// comment in build.go).
//
// The progress emitter is deliberately not part of this type: it is the
// Installer's own Emit field, forwarded through Download's emit parameter.
// Each entry point configures its emitter independently (CLI: stdout via
// printEvents; API: pluginHost.EmitPluginInstallProgress) without needing
// to fork this Source.
type CatalogArchiveSource struct {
	ID          string
	ArchiveURL  string
	SHA256      string
	Signature   []byte
	SignerKeyID string
	Downloader  *HTTPDownloader
}

// PluginID implements Source.
func (s *CatalogArchiveSource) PluginID() string { return s.ID }

// Download implements Source.
func (s *CatalogArchiveSource) Download(ctx context.Context, stagingDir string, emit EventFunc) (Handle, error) {
	downloader := s.Downloader
	if downloader == nil {
		downloader = &HTTPDownloader{}
	}
	// Download to a temp directory outside the staging tree to prevent the
	// archive from being committed into the final plugin directory. This
	// restores pre-convergence behavior where the archive was isolated from
	// the extraction target.
	tempDir, err := os.MkdirTemp("", "nanite-plugin-download-")
	if err != nil {
		return Handle{}, err
	}
	path, err := downloader.Download(ctx, s.ArchiveURL, tempDir, s.ID, emit)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return Handle{}, err
	}
	return Handle{
		Kind:           "archive",
		Path:           path,
		ExpectedSHA256: s.SHA256,
		Signature:      s.Signature,
		SignerKeyID:    s.SignerKeyID,
	}, nil
}
