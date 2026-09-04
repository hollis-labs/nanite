// Command frameworkgen regenerates the framework files that are derived from
// released dependencies and every content manifest used by safe upgrades.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/fsutil"
)

const (
	assetsPackagePath   = "internal/assets"
	frameworkSourcePath = "framework"
	currentManifestPath = "manifests/current.json"
	tesseractDocPath    = "framework/docs/tesseract-v0.9-contract.md"
	tesseractModule     = "github.com/hollis-labs/tesseract"
	tesseractGuide      = "docs/guides/tesseract-adoption-and-v0.9-migration.md"
	naniteRepository    = "github.com/hollis-labs/nanite"
)

var (
	currentRetiredPaths = []string{"docs/ref-conduit-plugin.md"}
	currentLegacyAllow  = []string{"docs/archive/cleanup-tasks.md", "docs/tesseract-v0.9-contract.md"}
	historicalManifests = []historicalManifestSpec{
		{
			Ref:          "a9a2067d972d7098e6640d9b2f985941106029c9",
			OutputPath:   "manifests/legacy-2.3.0.json",
			GoldenDigest: "8c18f9b42119c3e4f216750a61dfd45faec66a1f56eb98d69b329d9e4445bb94",
		},
	}
	markdownLinkPattern = regexp.MustCompile(`\]\(([^)\s]+)\)`)
)

type moduleInfo struct {
	Path    string
	Version string
	Dir     string
}

type generatedInput struct {
	Output    string `json:"output"`
	Module    string `json:"module"`
	Version   string `json:"version"`
	Path      string `json:"path"`
	SourceURL string `json:"source_url"`
}

type manifest struct {
	SchemaVersion    int               `json:"schema_version"`
	FrameworkVersion string            `json:"framework_version"`
	CorpusSource     string            `json:"corpus_source"`
	GeneratedInputs  []generatedInput  `json:"generated_inputs,omitempty"`
	Files            map[string]string `json:"files"`
	RetiredPaths     []string          `json:"retired_paths,omitempty"`
	LegacyAllowPaths []string          `json:"legacy_allow_paths,omitempty"`
}

type historicalManifestSpec struct {
	Ref          string
	OutputPath   string
	GoldenDigest string
}

type generatorPaths struct {
	RepoRoot        string
	AssetsDir       string
	FrameworkSource string
	CurrentManifest string
	TesseractDoc    string
}

func main() {
	checkOnly := flag.Bool("check", false, "fail instead of updating stale generated dependency snapshots and manifests")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(fmt.Errorf("unexpected positional arguments: %s", strings.Join(flag.Args(), " ")))
	}
	if err := run(*checkOnly); err != nil {
		fatal(err)
	}
}

func run(checkOnly bool) error {
	paths, err := canonicalPaths()
	if err != nil {
		return err
	}
	if inventoryErr := validateManifestInventory(paths.AssetsDir); inventoryErr != nil {
		return inventoryErr
	}
	info, err := resolveModule(paths.RepoRoot, tesseractModule)
	if err != nil {
		return err
	}
	if info.Version == "" || info.Dir == "" {
		return fmt.Errorf("%s is not pinned to a released module directory", tesseractModule)
	}
	guide, err := os.ReadFile(filepath.Join(info.Dir, filepath.FromSlash(tesseractGuide)))
	if err != nil {
		return fmt.Errorf("read %s@%s %s: %w", info.Path, info.Version, tesseractGuide, err)
	}
	generatedGuide, err := generateGuide(guide, info)
	if err != nil {
		return err
	}
	if emitErr := emit(paths.TesseractDoc, generatedGuide, checkOnly); emitErr != nil {
		return emitErr
	}

	current := manifestFromDirInputs(info)
	currentManifest, err := manifestFromDir(
		paths.FrameworkSource,
		naniteRepository+"/"+assetsPackagePath+"/"+frameworkSourcePath,
		current,
		sortedCopy(currentRetiredPaths),
		sortedCopy(currentLegacyAllow),
	)
	if err != nil {
		return err
	}
	encoded, err := encodeManifest(currentManifest)
	if err != nil {
		return err
	}
	if err := emit(paths.CurrentManifest, encoded, checkOnly); err != nil {
		return err
	}

	for _, spec := range historicalManifests {
		output := filepath.Join(paths.AssetsDir, filepath.FromSlash(spec.OutputPath))
		encodedLegacy, err := historicalManifestBytes(paths, spec, output)
		if err != nil {
			return err
		}
		if err := emit(output, encodedLegacy, checkOnly); err != nil {
			return err
		}
	}
	return nil
}

// historicalManifestBytes regenerates from the immutable Git object when it
// is available. Shallow CI checkouts intentionally may not have that object;
// there the checked-in manifest is a golden input whose exact digest is pinned
// in historicalManifests. This keeps go generate and -check deterministic and
// offline while still detecting corruption of every historical file entry.
func historicalManifestBytes(paths generatorPaths, spec historicalManifestSpec, output string) ([]byte, error) {
	if gitRefAvailable(paths.RepoRoot, spec.Ref) {
		legacy, err := manifestFromGit(paths.RepoRoot, spec.Ref, paths.FrameworkSource)
		if err != nil {
			return nil, err
		}
		encoded, err := encodeManifest(legacy)
		if err != nil {
			return nil, err
		}
		if got := digest(encoded); got != spec.GoldenDigest {
			return nil, fmt.Errorf("generated historical manifest %s digest %s does not match pinned golden %s", spec.OutputPath, got, spec.GoldenDigest)
		}
		return encoded, nil
	}
	// #nosec G304 -- output is a canonical checked-in manifest beneath the resolved repository root.
	golden, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("git object %s is unavailable and canonical historical manifest %s cannot be read: %w", spec.Ref, output, err)
	}
	if got := digest(golden); got != spec.GoldenDigest {
		return nil, fmt.Errorf("git object %s is unavailable and historical manifest %s digest %s does not match pinned golden %s", spec.Ref, output, got, spec.GoldenDigest)
	}
	return golden, nil
}

func gitRefAvailable(repoRoot, ref string) bool {
	// #nosec G204 -- ref is an immutable checked-in generator specification.
	cmd := exec.Command("git", "cat-file", "-e", ref+"^{commit}")
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func validateManifestInventory(assetsDir string) error {
	expected := map[string]bool{filepath.Base(currentManifestPath): true}
	for _, spec := range historicalManifests {
		name := filepath.Base(filepath.FromSlash(spec.OutputPath))
		if expected[name] {
			return fmt.Errorf("duplicate managed manifest output %s", name)
		}
		expected[name] = true
	}
	entries, err := os.ReadDir(filepath.Join(assetsDir, "manifests"))
	if err != nil {
		return fmt.Errorf("read manifest inventory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if !expected[entry.Name()] {
			return fmt.Errorf("manifest %s is not managed by frameworkgen", entry.Name())
		}
	}
	return nil
}

func canonicalPaths() (generatorPaths, error) {
	repoRoot, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return generatorPaths{}, err
	}
	repoRoot = filepath.Clean(strings.TrimSpace(repoRoot))
	assetsDir := filepath.Join(repoRoot, filepath.FromSlash(assetsPackagePath))
	return generatorPaths{
		RepoRoot:        repoRoot,
		AssetsDir:       assetsDir,
		FrameworkSource: filepath.Join(assetsDir, frameworkSourcePath),
		CurrentManifest: filepath.Join(assetsDir, currentManifestPath),
		TesseractDoc:    filepath.Join(assetsDir, tesseractDocPath),
	}, nil
}

func resolveModule(repoRoot, modulePath string) (moduleInfo, error) {
	// #nosec G204 -- the executable is fixed and modulePath is a package constant.
	cmd := exec.Command("go", "list", "-m", "-json", modulePath)
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		return moduleInfo{}, fmt.Errorf("resolve module %s: %w", modulePath, err)
	}
	var info moduleInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return moduleInfo{}, fmt.Errorf("decode module %s: %w", modulePath, err)
	}
	if info.Path != modulePath {
		return moduleInfo{}, fmt.Errorf("resolved module path %q, want %q", info.Path, modulePath)
	}
	return info, nil
}

func generateGuide(guide []byte, info moduleInfo) ([]byte, error) {
	rewritten, err := rewriteRelativeGuideLinks(guide, info)
	if err != nil {
		return nil, err
	}
	sourceURL := tesseractSourceURL(info.Version, tesseractGuide)
	header := fmt.Sprintf(
		"<!-- Generated dependency snapshot; DO NOT EDIT. -->\n"+
			"<!-- Upstream-owned source: %s@%s/%s -->\n"+
			"<!-- Stable source URL: %s -->\n"+
			"<!-- Relative upstream links are rewritten to tag-pinned URLs. -->\n\n",
		info.Path, info.Version, tesseractGuide, sourceURL,
	)
	return append([]byte(header), rewritten...), nil
}

func rewriteRelativeGuideLinks(guide []byte, info moduleInfo) ([]byte, error) {
	var rewriteErr error
	rewritten := markdownLinkPattern.ReplaceAllStringFunc(string(guide), func(match string) string {
		if rewriteErr != nil {
			return match
		}
		target := strings.TrimSuffix(strings.TrimPrefix(match, "]("), ")")
		if strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			return match
		}
		pathPart, fragment, _ := strings.Cut(target, "#")
		resolved := path.Clean(path.Join(path.Dir(tesseractGuide), pathPart))
		if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
			rewriteErr = fmt.Errorf("upstream guide link %q escapes module root", target)
			return match
		}
		moduleTarget := filepath.Join(info.Dir, filepath.FromSlash(resolved))
		if _, err := os.Stat(moduleTarget); err != nil {
			rewriteErr = fmt.Errorf("upstream guide link %q resolves to missing %s: %w", target, moduleTarget, err)
			return match
		}
		stable := tesseractSourceURL(info.Version, resolved)
		if fragment != "" {
			stable += "#" + fragment
		}
		return "](" + stable + ")"
	})
	if rewriteErr != nil {
		return nil, rewriteErr
	}
	return []byte(rewritten), nil
}

func tesseractSourceURL(version, sourcePath string) string {
	return "https://github.com/hollis-labs/tesseract/blob/" + version + "/" + sourcePath
}

func manifestFromDirInputs(info moduleInfo) []generatedInput {
	return []generatedInput{{
		Output:    "docs/tesseract-v0.9-contract.md",
		Module:    info.Path,
		Version:   info.Version,
		Path:      tesseractGuide,
		SourceURL: tesseractSourceURL(info.Version, tesseractGuide),
	}}
}

func manifestFromDir(root, corpusSource string, generatedInputs []generatedInput, retired, legacyAllow []string) (manifest, error) {
	// #nosec G304 -- root is the canonical framework source inside the repository.
	versionBytes, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return manifest{}, fmt.Errorf("read framework VERSION: %w", err)
	}
	m := manifest{
		SchemaVersion:    2,
		FrameworkVersion: strings.TrimSpace(string(versionBytes)),
		CorpusSource:     corpusSource,
		GeneratedInputs:  generatedInputs,
		Files:            make(map[string]string),
		RetiredPaths:     retired,
		LegacyAllowPaths: legacyAllow,
	}
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, filePath)
		if relErr != nil {
			return relErr
		}
		// #nosec G304 G122 -- filepath.WalkDir supplied a path under the canonical source root.
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return readErr
		}
		m.Files[filepath.ToSlash(rel)] = digest(data)
		return nil
	})
	if err != nil {
		return manifest{}, fmt.Errorf("scan framework source: %w", err)
	}
	return m, nil
}

func manifestFromGit(repoRoot, ref, workingSource string) (manifest, error) {
	absSource, err := filepath.Abs(workingSource)
	if err != nil {
		return manifest{}, err
	}
	relSource, err := filepath.Rel(repoRoot, absSource)
	if err != nil {
		return manifest{}, err
	}
	relSource = filepath.ToSlash(relSource)
	if relSource == ".." || strings.HasPrefix(relSource, "../") {
		return manifest{}, fmt.Errorf("framework source %s is outside repository %s", absSource, repoRoot)
	}
	pathsRaw, err := gitOutput(repoRoot, "ls-tree", "-r", "--name-only", ref, "--", relSource)
	if err != nil {
		return manifest{}, err
	}
	m := manifest{
		SchemaVersion: 2,
		CorpusSource:  "git:" + ref + ":" + relSource,
		Files:         make(map[string]string),
	}
	for _, repoPath := range strings.Split(strings.TrimSpace(pathsRaw), "\n") {
		if repoPath == "" {
			continue
		}
		data, err := gitBytes(repoRoot, "show", ref+":"+repoPath)
		if err != nil {
			return manifest{}, fmt.Errorf("git show %s:%s: %w", ref, repoPath, err)
		}
		rel := strings.TrimPrefix(repoPath, strings.TrimSuffix(relSource, "/")+"/")
		m.Files[rel] = digest(data)
		if rel == "VERSION" {
			m.FrameworkVersion = strings.TrimSpace(string(data))
		}
	}
	if m.FrameworkVersion == "" || len(m.Files) == 0 {
		return manifest{}, fmt.Errorf("git revision %s has no complete framework tree at %s", ref, relSource)
	}
	return m, nil
}

func gitOutput(repoRoot string, args ...string) (string, error) {
	output, err := gitBytes(repoRoot, args...)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func gitBytes(repoRoot string, args ...string) ([]byte, error) {
	// #nosec G204 -- this build-time generator constructs arguments from fixed operations and checked-in specs.
	cmd := exec.Command("git", args...)
	if repoRoot != "" {
		cmd.Dir = repoRoot
	}
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

func encodeManifest(m manifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func emit(outputPath string, want []byte, checkOnly bool) error {
	// #nosec G304 -- outputPath is a canonical checked-in generator output.
	got, err := os.ReadFile(outputPath)
	if err == nil && bytes.Equal(got, want) {
		return nil
	}
	if checkOnly {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read generated output %s: %w", outputPath, err)
		}
		return fmt.Errorf("generated output is stale: %s (run go generate ./internal/assets)", outputPath)
	}
	// #nosec G301 -- generated source directories must be readable by repository users.
	if mkdirErr := os.MkdirAll(filepath.Dir(outputPath), 0o755); mkdirErr != nil {
		return fmt.Errorf("create output directory for %s: %w", outputPath, mkdirErr)
	}
	if writeErr := fsutil.AtomicWriteFile(outputPath, want, 0o644); writeErr != nil {
		return fmt.Errorf("write generated output %s: %w", outputPath, writeErr)
	}
	return nil
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	for i := range out {
		out[i] = filepath.ToSlash(strings.TrimSpace(out[i]))
	}
	sort.Strings(out)
	return out
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "frameworkgen:", err)
	os.Exit(1)
}
