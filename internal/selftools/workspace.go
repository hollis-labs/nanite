package selftools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

const maxWorkspaceFile = 1 << 20

type workspace struct{ roots []string }

func protected(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		switch part {
		case ".nanite", ".ssh", ".gnupg", ".aws", ".kube", ".docker", ".netrc", ".codex", ".config", ".local",
			".bash_profile", ".bashrc", ".zshrc", ".zprofile", ".profile", ".git":
			return true
		}
		if strings.HasPrefix(part, ".claude") {
			return true
		}
	}
	return false
}

func (w workspace) resolve(path string) (*os.Root, string, error) {
	if path == "" {
		return nil, "", fmt.Errorf("path required")
	}
	var last error
	for _, base := range w.roots {
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, abs)
		}
		rel, err := filepath.Rel(base, abs)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		if protected(rel) {
			return nil, "", fmt.Errorf("protected workspace path")
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			last = err
			continue
		}
		if err := verifyPath(root, rel); err != nil {
			_ = root.Close()
			return nil, "", err
		}
		return root, rel, nil
	}
	if last != nil {
		return nil, "", last
	}
	return nil, "", fmt.Errorf("path is outside the workspace")
}

func verifyPath(root *os.Root, path string) error {
	current := ""
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace path must not traverse a symbolic link")
		}
	}
	return nil
}

type pathArgs struct{ Path string }
type writeArgs struct{ Path, Content string }
type editArgs struct{ Path, Old, New string }

func (w workspace) read(ctx context.Context, args pathArgs) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, path, err := w.resolve(args.Path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return readRegular(root, path)
}

func readRegular(root *os.Root, path string) (string, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxWorkspaceFile {
		return "", fmt.Errorf("path must be a regular file at most 1 MiB")
	}
	file, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", fmt.Errorf("file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxWorkspaceFile+1))
	if len(raw) > maxWorkspaceFile {
		return "", fmt.Errorf("file exceeds 1 MiB")
	}
	return string(raw), err
}

func (w workspace) list(ctx context.Context, args pathArgs) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, path, err := w.resolve(args.Path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(path))
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if protected(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	return strings.Join(names, "\n"), nil
}

func (w workspace) write(ctx context.Context, args writeArgs) (string, error) {
	root, path, err := w.resolve(args.Path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return "File written.", writeRegular(ctx, root, path, args.Content)
}

func (w workspace) edit(ctx context.Context, args editArgs) (string, error) {
	root, path, err := w.resolve(args.Path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	original, err := readRegular(root, path)
	if err != nil {
		return "", err
	}
	if args.Old == "" || strings.Count(original, args.Old) != 1 {
		return "", fmt.Errorf("old text must match exactly once")
	}
	return "File edited.", writeRegular(ctx, root, path, strings.Replace(original, args.Old, args.New, 1))
}

func writeRegular(ctx context.Context, root *os.Root, path, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(text) > maxWorkspaceFile {
		return fmt.Errorf("content exceeds 1 MiB")
	}
	mode := os.FileMode(0600)
	info, err := root.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("path must be a regular file")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp := filepath.Join(filepath.Dir(path), ".nanite-write-"+store.NewID(time.Now()))
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	if _, err := io.WriteString(file, text); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(temp, path)
}
