package plugin

import (
	"runtime"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"

	sharedcatalog "github.com/hollis-labs/plugins-catalog"
)

// DecodeCatalog validates the released catalog schema and selects this host's
// platform archive. Other hosts' plugins are outside Nanite's catalog view.
func DecodeCatalog(raw []byte) (*CatalogFile, error) {
	document, err := sharedcatalog.Decode(raw)
	if err != nil {
		return nil, err
	}
	result := &CatalogFile{SchemaVersion: document.SchemaVersion, CatalogVersion: document.CatalogVersion, Plugins: []CatalogEntry{}}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	for _, declaration := range document.Plugins {
		hostRange, ok := declaration.Hosts["nanite"]
		if !ok {
			continue
		}
		if rangeErr := CheckHostRange(manifest.HostRange{Min: hostRange.Min, Max: hostRange.Max}); rangeErr != nil {
			continue
		}
		if idErr := ValidatePluginID(declaration.ID); idErr != nil {
			return nil, idErr
		}
		entry := CatalogEntry{ID: declaration.ID, Name: declaration.Name, Version: declaration.Version, Description: declaration.Description, Repo: declaration.Source.Repo, Tags: append([]string(nil), declaration.Directory.Tags...), ManifestSHA256: declaration.ManifestSHA256, Runtime: "subprocess", Summary: declaration.Summary}
		for _, archive := range declaration.Archives {
			if archive.Platform == platform {
				entry.ArchiveURL = archive.URL
				entry.Checksum = archive.SHA256
				entry.ArchiveSize = archive.Size
				entry.Available = true
				break
			}
		}
		result.Plugins = append(result.Plugins, entry)
	}
	return result, nil
}
