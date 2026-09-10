package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoricalManifestsMatchImmutableGitTrees(t *testing.T) {
	paths, err := canonicalPaths()
	if err != nil {
		t.Fatalf("resolve canonical paths: %v", err)
	}
	if err := validateManifestInventory(paths.AssetsDir); err != nil {
		t.Fatalf("checked-in manifest is outside the canonical generator inventory: %v", err)
	}
	for _, spec := range historicalManifests {
		t.Run(spec.OutputPath, func(t *testing.T) {
			output := filepath.Join(paths.AssetsDir, filepath.FromSlash(spec.OutputPath))
			want, err := historicalManifestBytes(paths, spec, output)
			if err != nil {
				t.Fatalf("generate or verify historical manifest: %v", err)
			}
			// #nosec G304 -- output is a canonical checked-in manifest beneath the resolved repository root.
			got, err := os.ReadFile(output)
			if err != nil {
				t.Fatalf("read checked-in historical manifest: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("historical manifest %s drifted from complete git tree %s", spec.OutputPath, spec.Ref)
			}
			if digest(got) != spec.GoldenDigest {
				t.Fatalf("historical manifest %s does not match pinned golden digest", spec.OutputPath)
			}
		})
	}
}

func TestHistoricalManifestGoldenFallbackWorksWithoutGitObject(t *testing.T) {
	paths, err := canonicalPaths()
	if err != nil {
		t.Fatalf("resolve canonical paths: %v", err)
	}
	spec := historicalManifests[0]
	canonicalPath := filepath.Join(paths.AssetsDir, filepath.FromSlash(spec.OutputPath))
	// #nosec G304 -- canonicalPath is a checked-in manifest beneath the resolved repository root.
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("read canonical historical manifest: %v", err)
	}
	shallowRoot := t.TempDir()
	initCmd := exec.Command("git", "init", "-q")
	initCmd.Dir = shallowRoot
	if output, initErr := initCmd.CombinedOutput(); initErr != nil {
		t.Fatalf("initialize shallow-object test repository: %v\n%s", initErr, output)
	}
	shallowOutput := filepath.Join(shallowRoot, "legacy.json")
	if emitErr := emit(shallowOutput, canonical, false); emitErr != nil {
		t.Fatalf("seed historical golden: %v", emitErr)
	}
	shallowPaths := generatorPaths{RepoRoot: shallowRoot}
	got, err := historicalManifestBytes(shallowPaths, spec, shallowOutput)
	if err != nil {
		t.Fatalf("verify historical golden without Git object: %v", err)
	}
	if !bytes.Equal(got, canonical) {
		t.Fatal("historical golden fallback changed canonical bytes")
	}
	if emitErr := emit(shallowOutput, []byte("{}\n"), false); emitErr != nil {
		t.Fatalf("corrupt historical golden fixture: %v", emitErr)
	}
	if _, err := historicalManifestBytes(shallowPaths, spec, shallowOutput); err == nil {
		t.Fatal("corrupt historical golden passed without its Git object")
	}
}

func TestGeneratedGuideUsesUpstreamOwnedStableLinks(t *testing.T) {
	paths, err := canonicalPaths()
	if err != nil {
		t.Fatalf("resolve canonical paths: %v", err)
	}
	info, err := resolveModule(paths.RepoRoot, tesseractModule)
	if err != nil {
		t.Fatalf("resolve Tesseract module: %v", err)
	}
	upstream, err := os.ReadFile(filepath.Join(info.Dir, filepath.FromSlash(tesseractGuide)))
	if err != nil {
		t.Fatalf("read upstream guide: %v", err)
	}
	generated, err := generateGuide(upstream, info)
	if err != nil {
		t.Fatalf("generate guide: %v", err)
	}
	text := string(generated)
	if !strings.Contains(text, "Upstream-owned source:") {
		t.Fatal("generated guide does not identify upstream ownership")
	}
	if strings.Contains(text, "](../") || strings.Contains(text, "](./") {
		t.Fatal("generated guide retains a relative upstream link")
	}
	for _, stable := range []string{
		"https://github.com/hollis-labs/tesseract/blob/v0.10.0/examples/adoption-go/main.go",
		"https://github.com/hollis-labs/tesseract/blob/v0.10.0/docs/MCP_TOOLS.md",
		"https://github.com/hollis-labs/tesseract/blob/v0.10.0/docs/QUICKSTART.md",
	} {
		if !strings.Contains(text, stable) {
			t.Errorf("generated guide is missing stable upstream link %s", stable)
		}
	}
}

func TestCheckWorksFromRepositoryAndPackageDirectories(t *testing.T) {
	paths, err := canonicalPaths()
	if err != nil {
		t.Fatalf("resolve canonical paths: %v", err)
	}
	for _, test := range []struct {
		name string
		dir  string
		args []string
	}{
		{name: "repository-root", dir: paths.RepoRoot, args: []string{"run", "./internal/assets/cmd/frameworkgen", "-check"}},
		{name: "package", dir: paths.AssetsDir, args: []string{"run", "./cmd/frameworkgen", "-check"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("go", test.args...) // #nosec G204 -- fixed test command and checked canonical paths.
			cmd.Dir = test.dir
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("frameworkgen --check from %s: %v\n%s", test.name, err, output)
			}
		})
	}
}
