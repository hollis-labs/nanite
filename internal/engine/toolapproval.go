package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/store"
)

// canonicalLocation resolves existing ancestors even when the final directory
// has not been created. A dangling symlink is refused, not treated as missing.
func canonicalLocation(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(absolute)
		if err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if _, statErr := os.Lstat(absolute); statErr == nil {
			return "", err
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", err
		}
		tail = append([]string{filepath.Base(absolute)}, tail...)
		absolute = parent
	}
}

func (e *Engine) toolApprovalLocation() (string, string, error) {
	agentDir, err := filepath.EvalSymlinks(e.cfg.Dir)
	if err != nil {
		return "", "", err
	}
	agentDir, err = filepath.Abs(agentDir)
	if err != nil {
		return "", "", err
	}
	dir := e.cfg.ToolApprovalDir
	if dir == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return "", "", err
		}
		dir = filepath.Join(config, "nanite", "tool-approvals")
	}
	dir, err = canonicalLocation(dir)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(agentDir, dir)
	if err != nil {
		return "", "", err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("tool approvals must be stored outside the agent directory")
	}
	return dir, agentDir, nil
}

func approvalFile(agentDir string, pkg authored.ToolPackage) string {
	hash := sha256.Sum256([]byte(agentDir + "\x00" + pkg.Name + "\x00" + pkg.Digest))
	return hex.EncodeToString(hash[:]) + ".approved"
}

// toolApproved trusts only the external host store. Old agent-local settings
// are deliberately ignored; importing them would preserve the seeding flaw.
func (e *Engine) toolApproved(ctx context.Context, pkg authored.ToolPackage) bool {
	if ctx.Err() != nil || pkg.Digest == "" {
		return false
	}
	dir, agentDir, err := e.toolApprovalLocation()
	if err != nil {
		return false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(approvalFile(agentDir, pkg), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(pkg.Digest)+1) {
		return false
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(len(pkg.Digest)+2)))
	return err == nil && string(raw) == pkg.Digest+"\n"
}

func (e *Engine) approveTool(ctx context.Context, pkg authored.ToolPackage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, _, err := e.toolApprovalLocation()
	if err != nil {
		return err
	}
	if err := store.PreparePrivateDir(dir); err != nil {
		return err
	}
	// Resolve once more after creation, before obtaining the directory handle.
	dir, agentDir, err := e.toolApprovalLocation()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	temp := ".approve-" + store.NewID(time.Now())
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = io.WriteString(f, pkg.Digest+"\n"); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(temp, approvalFile(agentDir, pkg))
}
