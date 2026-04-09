// Package install contains the installer service used by the `nanite install`
// CLI entry point. This file implements the scaffolding step that initializes
// a project directory with a `.nanite/` config tree and a root `NANITE.md`
// boot prompt.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"

	"github.com/hollis-labs/nanite/internal/assets"
)

// ScaffoldSource holds values used to render scaffold templates.
type ScaffoldSource struct {
	FrameworkVersion string
	ProjectName      string
}

// ScaffoldNaniteDir creates projectDir/.nanite/ with config.yaml (from the
// embedded template), an empty agents/ subdirectory, and symlinks into
// globalHome for roles/, skills/, and commands/. The operation is idempotent:
// existing files are preserved, existing symlinks (or anything at the link
// paths) are not replaced, and the directory structure is created with
// MkdirAll.
func ScaffoldNaniteDir(projectDir, globalHome string, src ScaffoldSource) error {
	naniteDir := filepath.Join(projectDir, ".nanite")
	if err := os.MkdirAll(naniteDir, 0o755); err != nil {
		return fmt.Errorf("mkdir .nanite: %w", err)
	}

	// config.yaml — only if missing.
	cfgPath := filepath.Join(naniteDir, "config.yaml")
	if _, err := os.Stat(cfgPath); errors.Is(err, fs.ErrNotExist) {
		tmplBytes, err := assets.File("templates/nanite-config.yaml.tmpl")
		if err != nil {
			return fmt.Errorf("read config template: %w", err)
		}
		rendered, err := renderTemplate("config.yaml", string(tmplBytes), src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(cfgPath, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("write config.yaml: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("stat config.yaml: %w", err)
	}

	// agents/ — empty but present.
	if err := os.MkdirAll(filepath.Join(naniteDir, "agents"), 0o755); err != nil {
		return fmt.Errorf("mkdir agents: %w", err)
	}

	// Symlinks into globalHome. Anything already present at the link path is
	// left alone to preserve the idempotency contract.
	for _, sub := range []string{"roles", "skills", "commands"} {
		link := filepath.Join(naniteDir, sub)
		target := filepath.Join(globalHome, sub)
		if _, err := os.Lstat(link); err == nil {
			continue // already exists — don't replace
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("lstat %s: %w", link, err)
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
		}
	}

	return nil
}

// ScaffoldNaniteMD writes projectDir/NANITE.md from the embedded template.
// If a NANITE.md already exists at that path, returns without touching it —
// humans own the file and the installer is only for initial scaffolding.
func ScaffoldNaniteMD(projectDir string, src ScaffoldSource) error {
	path := filepath.Join(projectDir, "NANITE.md")
	if _, err := os.Stat(path); err == nil {
		return nil // preserve existing
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat NANITE.md: %w", err)
	}
	tmplBytes, err := assets.File("templates/NANITE.md.tmpl")
	if err != nil {
		return fmt.Errorf("read NANITE.md template: %w", err)
	}
	rendered, err := renderTemplate("NANITE.md", string(tmplBytes), src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write NANITE.md: %w", err)
	}
	return nil
}

// renderTemplate parses body as a text/template and executes it against data.
func renderTemplate(name, body string, data any) (string, error) {
	t, err := template.New(name).Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %s: %w", name, err)
	}
	return buf.String(), nil
}
