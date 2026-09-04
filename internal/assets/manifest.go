package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

const currentManifestPath = "manifests/current.json"

type contentManifest struct {
	SchemaVersion    int               `json:"schema_version"`
	FrameworkVersion string            `json:"framework_version"`
	CorpusSource     string            `json:"corpus_source"`
	GeneratedInputs  []generatedInput  `json:"generated_inputs,omitempty"`
	Files            map[string]string `json:"files"`
	RetiredPaths     []string          `json:"retired_paths,omitempty"`
	LegacyAllowPaths []string          `json:"legacy_allow_paths,omitempty"`
}

type generatedInput struct {
	Output    string `json:"output"`
	Module    string `json:"module"`
	Version   string `json:"version"`
	Path      string `json:"path"`
	SourceURL string `json:"source_url"`
}

type manifestSet struct {
	current    contentManifest
	historical []contentManifest
}

func loadManifestSet() (manifestSet, error) {
	current, err := loadManifest(currentManifestPath)
	if err != nil {
		return manifestSet{}, err
	}
	entries, err := fs.ReadDir(frameworkAssets, "manifests")
	if err != nil {
		return manifestSet{}, fmt.Errorf("read embedded manifests: %w", err)
	}
	set := manifestSet{current: current}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "current.json" || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		manifest, err := loadManifest("manifests/" + entry.Name())
		if err != nil {
			return manifestSet{}, err
		}
		set.historical = append(set.historical, manifest)
	}
	sort.Slice(set.historical, func(i, j int) bool {
		return set.historical[i].FrameworkVersion < set.historical[j].FrameworkVersion
	})
	if set.current.FrameworkVersion != Version() {
		return manifestSet{}, fmt.Errorf("current manifest version %q does not match embedded framework %q", set.current.FrameworkVersion, Version())
	}
	return set, nil
}

func loadManifest(path string) (contentManifest, error) {
	data, err := frameworkAssets.ReadFile(path)
	if err != nil {
		return contentManifest{}, fmt.Errorf("read embedded manifest %s: %w", path, err)
	}
	var manifest contentManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return contentManifest{}, fmt.Errorf("decode embedded manifest %s: %w", path, err)
	}
	if manifest.SchemaVersion != 2 || manifest.FrameworkVersion == "" || manifest.CorpusSource == "" || len(manifest.Files) == 0 {
		return contentManifest{}, fmt.Errorf("embedded manifest %s is incomplete or unsupported", path)
	}
	for rel, digest := range manifest.Files {
		if !validRelativeAssetPath(rel) || len(digest) != sha256.Size*2 {
			return contentManifest{}, fmt.Errorf("embedded manifest %s contains invalid file entry %q", path, rel)
		}
	}
	for _, rel := range manifest.RetiredPaths {
		if !validRelativeAssetPath(rel) {
			return contentManifest{}, fmt.Errorf("embedded manifest %s contains invalid retired path %q", path, rel)
		}
	}
	for _, rel := range manifest.LegacyAllowPaths {
		if !validRelativeAssetPath(rel) {
			return contentManifest{}, fmt.Errorf("embedded manifest %s contains invalid legacy allow path %q", path, rel)
		}
	}
	return manifest, nil
}

func validRelativeAssetPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, `\`) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func (set manifestSet) isHistoricalStock(path string, data []byte) bool {
	digest := contentDigest(data)
	for _, manifest := range set.historical {
		if manifest.Files[path] == digest {
			return true
		}
	}
	return false
}

func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
