package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/plugin"
)

// BuildOptions bundles the entry-point-specific pieces every install caller
// (CLI, GUI/API) must supply. NewInstaller is the one canonical Installer
// construction site — both cmd/nanite's buildInstaller and internal/api's
// catalog-install handler call this instead of each hand-rolling its own
// Installer wiring. A second hand-rolled wiring site is exactly how the
// GUI/API catalog-install path diverged from the CLI's pipeline in the
// first place (docs/audits/2026-08-21-go-quality/REPORT.md §8.6,
// GO-PLUGIN-003; AD-04 item 6,
// TASKS/audit-remediation/01-plugin-install-convergence/01-unify-plugin-catalog-install-pipeline.md).
type BuildOptions struct {
	// Extractor materializes a downloaded/local Handle into the staging
	// dir. Required — callers differ (CLI: tar.gz only via
	// TarGzExtractor; API: a format-dispatching zip/tar.gz extractor, see
	// internal/api/catalog.go).
	Extractor Extractor

	// Loader hands the committed plugin dir to the runtime. Required —
	// callers differ (CLI: an out-of-process no-op; API: an in-process
	// hot-load adapter around pms.runPluginLoadIntoHost).
	Loader Loader

	// StagingRoot/PluginsRoot configure the DirStaging used for the
	// install's atomic commit. Both required, and must live on the same
	// filesystem (DirStaging.Commit's own requirement).
	StagingRoot string
	PluginsRoot string

	// Emit streams install.Event progress to the caller (CLI: stdout;
	// API: pluginHost.EmitPluginInstallProgress). Optional — nil is a
	// valid no-op emitter.
	Emit EventFunc
}

// NewInstaller builds an Installer + its DirStaging from opts, using the
// standard ChecksumVerifier + manifest ManifestValidator wiring every
// entry point shares. Only Extractor and Loader are entry-point-specific;
// everything else is the one canonical shape.
func NewInstaller(opts BuildOptions) (*Installer, *DirStaging) {
	staging := &DirStaging{StagingRoot: opts.StagingRoot, PluginsRoot: opts.PluginsRoot}
	inst := &Installer{
		Verifier:  &ChecksumVerifier{},
		Extractor: opts.Extractor,
		Validator: &ManifestValidator{},
		Loader:    opts.Loader,
		Staging:   staging,
		Emit:      opts.Emit,
	}
	return inst, staging
}

// ManifestValidator adapts ValidateManifest into the Validator interface.
// The one shared manifest-validation step both cmd/nanite's CLI installer
// (formerly its own private cliValidator) and internal/api's catalog
// installer use.
type ManifestValidator struct{}

// Validate implements Validator.
func (ManifestValidator) Validate(ctx context.Context, pluginDir string) error {
	manifestPath := filepath.Join(pluginDir, "plugin.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		return fmt.Errorf("plugin.yaml not found at %s", manifestPath)
	}
	if verr := ValidateManifest(manifestPath, pluginDir, ValidationOptions{}); verr != nil {
		if verr.HasRefusals() {
			return fmt.Errorf("manifest validation failed: %s", verr.Error())
		}
	}
	return nil
}

// ValidateIdentity prevents installing a valid bundle under another plugin's ID.
func (ManifestValidator) ValidateIdentity(ctx context.Context, directory, expectedID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	declared, err := plugin.ParseManifest(filepath.Join(directory, "plugin.yaml"))
	if err != nil {
		return err
	}
	if declared.ID != expectedID {
		return fmt.Errorf("manifest ID %q differs from requested %q", declared.ID, expectedID)
	}
	return nil
}
