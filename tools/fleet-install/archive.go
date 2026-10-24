package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxExtract = 2 << 30 // total bytes extracted from one tarball

// SHA256File returns the hex sha256 of a file.
func SHA256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ParseSums reads `<sha256>  <file>` lines (a leading `*` or `./` on the name is tolerated).
func ParseSums(p string) (map[string]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, fmt.Errorf("%s: malformed line %q", filepath.Base(p), line)
		}
		name := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		m[name] = strings.ToLower(fields[0])
	}
	return m, sc.Err()
}

// VerifySum checks file against the sums map by base name; a missing entry is a refusal.
func VerifySum(sums map[string]string, file string) error {
	name := filepath.Base(file)
	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("checksum refused: %s has no entry in SHA256SUMS", name)
	}
	got, err := SHA256File(file)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum refused: %s sha256 mismatch (expected %s, got %s)", name, want[:12], got[:12])
	}
	return nil
}

// ExtractTarGz unpacks a .tar.gz into dest. Only regular files and directories are
// accepted; absolute paths, "..", links and devices are rejected.
func ExtractTarGz(tarball, dest string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: not a gzip file: %v", filepath.Base(tarball), err)
	}
	defer gz.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading tarball: %v", err)
		}
		name := h.Name
		clean := path.Clean(strings.TrimPrefix(name, "./"))
		if name == "" || path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\\") {
			return fmt.Errorf("tarball rejected: unsafe path %q", name)
		}
		if clean == "." {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if h.Size < 0 || total > maxExtract {
				return fmt.Errorf("tarball rejected: too large")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return fmt.Errorf("tarball rejected: %v", err)
			}
			if _, err := io.Copy(out, io.LimitReader(tr, h.Size)); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("tarball rejected: %q has unsupported entry type %q (links and devices are not allowed)", name, string(h.Typeflag))
		}
	}
}
