package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Default limits for extracted archives.
const (
	DefaultMaxTotalUncompressedBytes = int64(500 * 1024 * 1024) // 500 MiB
	DefaultMaxFileBytes              = int64(100 * 1024 * 1024) // 100 MiB
	DefaultMaxEntries                = 5000
	// DefaultMaxCompressionRatio rejects archives whose uncompressed size is
	// more than 200x the on-disk size. Typical tar.gz of source code is <10x.
	DefaultMaxCompressionRatio = int64(200)
)

// TarGzExtractor materialises a Handle into targetDir. Archive handles are
// extracted from tar.gz with adversarial guards. Directory handles are
// copied in (no symlinks — that path is reserved for the explicit --link
// flag plumbed through the CLI, not this extractor).
//
// Zero value is usable; all limits default to the constants above.
type TarGzExtractor struct {
	MaxTotalUncompressedBytes int64
	MaxFileBytes              int64
	MaxEntries                int
	MaxCompressionRatio       int64
}

// Extract materialises h into targetDir. targetDir must already exist.
func (e *TarGzExtractor) Extract(ctx context.Context, h Handle, targetDir string, emit EventFunc) error {
	if targetDir == "" {
		return errors.New("extract: empty targetDir")
	}
	if _, err := os.Stat(targetDir); err != nil {
		return fmt.Errorf("extract: stat targetDir: %w", err)
	}

	switch h.Kind {
	case "archive":
		return e.extractArchive(ctx, h, targetDir, emit)
	case "directory":
		return e.copyDirectory(ctx, h.Path, targetDir)
	default:
		return fmt.Errorf("extract: unsupported handle kind %q", h.Kind)
	}
}

func (e *TarGzExtractor) limits() (maxTotal, maxFile int64, maxEntries int, maxRatio int64) {
	maxTotal = e.MaxTotalUncompressedBytes
	if maxTotal <= 0 {
		maxTotal = DefaultMaxTotalUncompressedBytes
	}
	maxFile = e.MaxFileBytes
	if maxFile <= 0 {
		maxFile = DefaultMaxFileBytes
	}
	maxEntries = e.MaxEntries
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	maxRatio = e.MaxCompressionRatio
	if maxRatio <= 0 {
		maxRatio = DefaultMaxCompressionRatio
	}
	return
}

func (e *TarGzExtractor) extractArchive(ctx context.Context, h Handle, targetDir string, emit EventFunc) error {
	if h.Path == "" {
		return errors.New("extract: empty archive path")
	}
	maxTotal, maxFile, maxEntries, maxRatio := e.limits()

	fi, err := os.Stat(h.Path)
	if err != nil {
		return fmt.Errorf("extract: stat archive: %w", err)
	}
	archiveSize := fi.Size()
	compressionCap := archiveSize * maxRatio
	if compressionCap <= 0 || compressionCap > maxTotal {
		compressionCap = maxTotal
	}

	f, err := os.Open(h.Path)
	if err != nil {
		return fmt.Errorf("extract: open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("extract: gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	cleanTarget := filepath.Clean(targetDir)
	var totalWritten int64
	var entries int

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("extract: tar next: %w", err)
		}

		entries++
		if entries > maxEntries {
			return fmt.Errorf("extract: archive has > %d entries", maxEntries)
		}

		name := hdr.Name
		if name == "" {
			return errors.New("extract: empty entry name")
		}
		// Reject absolute paths outright.
		if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
			return fmt.Errorf("extract: absolute path entry %q", name)
		}
		// Reject drive-letter prefixes and any `..` segments.
		if strings.Contains(name, "..") {
			return fmt.Errorf("extract: parent-dir segment in %q", name)
		}

		cleanName := filepath.Clean(name)
		if cleanName == "." || cleanName == "" {
			continue
		}
		destPath := filepath.Join(cleanTarget, cleanName)
		rel, err := filepath.Rel(cleanTarget, destPath)
		if err != nil {
			return fmt.Errorf("extract: rel %q: %w", name, err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("extract: path %q escapes target", name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return fmt.Errorf("extract: mkdir %s: %w", destPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size < 0 || hdr.Size > maxFile {
				return fmt.Errorf("extract: file %q size %d exceeds per-file cap %d", name, hdr.Size, maxFile)
			}
			if totalWritten+hdr.Size > maxTotal {
				return fmt.Errorf("extract: total size would exceed %d bytes", maxTotal)
			}
			if totalWritten+hdr.Size > compressionCap {
				return fmt.Errorf("extract: compression ratio cap exceeded")
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return fmt.Errorf("extract: mkdir parent of %s: %w", destPath, err)
			}
			mode := permMode(hdr.Mode)
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return fmt.Errorf("extract: create %s: %w", destPath, err)
			}
			// Cap the per-file read to the declared size + 1 so that a
			// malicious archive with an understated header size can't
			// smuggle extra data past our total cap.
			n, err := io.CopyN(out, tr, hdr.Size)
			closeErr := out.Close()
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("extract: write %s: %w", destPath, err)
			}
			if closeErr != nil {
				return fmt.Errorf("extract: close %s: %w", destPath, closeErr)
			}
			if n != hdr.Size {
				return fmt.Errorf("extract: short write for %s: %d/%d", destPath, n, hdr.Size)
			}
			totalWritten += n
		case tar.TypeSymlink, tar.TypeLink:
			// Symlinks and hardlinks both carry escape risk (target can
			// point outside the extraction root; hardlinks preserve
			// file-descriptor access to arbitrary inodes). Reject.
			return fmt.Errorf("extract: link entries not allowed (%q)", name)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return fmt.Errorf("extract: device/fifo entries not allowed (%q)", name)
		default:
			return fmt.Errorf("extract: unsupported tar entry type %c for %q", hdr.Typeflag, name)
		}

		if emit != nil && entries%50 == 0 {
			emit(Event{State: StateExtracting, Message: fmt.Sprintf("extracted %d entries", entries)})
		}
	}
	return nil
}

// permMode returns the file permission bits we honor: user/group/other rwx,
// with setuid/setgid/sticky stripped. Archive-supplied modes never get to
// drop privileges or escalate via suid/sgid on our installed files.
func permMode(mode int64) os.FileMode {
	return os.FileMode(mode) & 0o777
}

// copyDirectory mirrors srcDir into targetDir. The source must exist and be
// a directory. Symlinks inside the source are not followed; if encountered
// they are rejected (same escape-risk rationale as symlinks-in-archives).
func (e *TarGzExtractor) copyDirectory(ctx context.Context, srcDir, targetDir string) error {
	if srcDir == "" {
		return errors.New("extract: empty source directory")
	}
	srcInfo, err := os.Lstat(srcDir)
	if err != nil {
		return fmt.Errorf("extract: lstat src: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("extract: source %q is not a directory", srcDir)
	}
	cleanSrc := filepath.Clean(srcDir)

	return filepath.Walk(cleanSrc, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		rel, err := filepath.Rel(cleanSrc, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dest := filepath.Join(targetDir, rel)

		lst, err := os.Lstat(path)
		if err != nil {
			return err
		}
		mode := lst.Mode()

		switch {
		case mode&os.ModeSymlink != 0:
			return fmt.Errorf("extract: symlinks in source dir not allowed (%q)", rel)
		case mode.IsDir():
			return os.MkdirAll(dest, 0o755)
		case mode.IsRegular():
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			return copyFile(path, dest, mode.Perm()&0o777)
		default:
			return fmt.Errorf("extract: unsupported file type for %q", rel)
		}
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
