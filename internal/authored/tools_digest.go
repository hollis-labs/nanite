package authored

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

type fileVersion struct {
	Size, Modified, Changed int64
	Mode                    fs.FileMode
	Device, Inode           uint64
}

func versionOf(info fs.FileInfo) fileVersion {
	if info == nil {
		return fileVersion{}
	}
	v := fileVersion{Size: info.Size(), Modified: info.ModTime().UnixNano(), Mode: info.Mode()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		v.Device = uint64(st.Dev) //nolint:unconvert // Dev is signed on Darwin.
		v.Inode = st.Ino
		v.Changed = statChangeTime(st)
	}
	return v
}

// Open without waiting for a FIFO, refuse symlinks and verify the opened
// object itself. A stale directory entry never authorizes reading another type.
func readPackageFile(root *os.Root, p string, expected fs.FileInfo, limit int64) ([]byte, error) {
	f, err := root.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	if versionOf(before) != versionOf(expected) {
		return nil, fmt.Errorf("%s changed before reading", p)
	}
	if before.Size() > limit {
		return nil, fmt.Errorf("%s exceeds the package byte limit", p)
	}
	data, err := readPackageBytes(f, limit)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if versionOf(before) != versionOf(after) || int64(len(data)) != before.Size() {
		return nil, fmt.Errorf("%s changed while reading", p)
	}
	return data, nil
}

func readPackageBytes(r io.Reader, limit int64) ([]byte, error) {
	// One sentinel byte detects growth beyond the remaining budget.
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("package exceeds its byte limit")
	}
	return data, nil
}

type packageNode struct {
	path, rel, target string
	info              fs.FileInfo
}
type digestRecord struct {
	signature [32]byte
	digest    string
}

var toolDigests = struct {
	sync.Mutex
	entries map[string]digestRecord
}{entries: map[string]digestRecord{}}

const maxCachedToolDigests = 256

// packageNodes fingerprints paths, size, mtime, inode, mode and change time.
// Change time also invalidates a same-size rewrite whose mtime was restored.
func packageNodes(root *os.Root, dir string) ([]packageNode, [32]byte, error) {
	var nodes []packageNode
	h := sha256.New()
	total := int64(0)
	err := fs.WalkDir(root.FS(), dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		n := packageNode{path: p, rel: strings.TrimPrefix(strings.TrimPrefix(p, dir), "/"), info: info}
		switch {
		case info.IsDir():
		case info.Mode()&fs.ModeSymlink != 0:
			n.target, err = root.Readlink(p)
			if err != nil {
				return err
			}
		case info.Mode().IsRegular():
			total += info.Size()
			if total > MaxToolPackageBytes {
				return fmt.Errorf("the package is larger than %d MiB", MaxToolPackageBytes>>20)
			}
		default:
			return fmt.Errorf("%s is not a regular file, directory or symlink", n.rel)
		}
		_, _ = fmt.Fprintf(h, "%q\x00%+v\x00%q\x00", n.rel, versionOf(info), n.target)
		nodes = append(nodes, n)
		return nil
	})
	var signature [32]byte
	copy(signature[:], h.Sum(nil))
	return nodes, signature, err
}

// packageDigest retains the original content digest format. Only a bounded
// cache of metadata fingerprints and final digests survives between calls.
func packageDigest(root *os.Root, dir string) (string, error) {
	return packageDigestWithReader(root, dir, readPackageFile)
}

// Inject the read operation to exercise changes between the two metadata
// snapshots deterministically. Production always uses readPackageFile.
func packageDigestWithReader(root *os.Root, dir string, read func(*os.Root, string, fs.FileInfo, int64) ([]byte, error)) (string, error) {
	nodes, signature, err := packageNodes(root, dir)
	if err != nil {
		return "", fmt.Errorf("digest: %w", err)
	}
	name, err := filepath.Abs(root.Name())
	if err != nil {
		return "", err
	}
	key := filepath.Join(name, filepath.FromSlash(dir))
	toolDigests.Lock()
	cached, hit := toolDigests.entries[key]
	toolDigests.Unlock()
	if hit && cached.signature == signature {
		return cached.digest, nil
	}
	h := sha256.New()
	remaining := int64(MaxToolPackageBytes)
	for _, n := range nodes {
		switch {
		case n.info.IsDir():
			_, _ = fmt.Fprintf(h, "dir\x00%s\x00", n.rel)
		case n.info.Mode()&fs.ModeSymlink != 0:
			_, _ = fmt.Fprintf(h, "link\x00%s\x00%s\x00", n.rel, n.target)
		default:
			data, err := read(root, n.path, n.info, remaining)
			if err != nil {
				return "", fmt.Errorf("digest: %w", err)
			}
			remaining -= int64(len(data))
			_, _ = fmt.Fprintf(h, "file\x00%s\x00%d\x00%o\x00", n.rel, len(data), n.info.Mode().Perm())
			_, _ = h.Write(data)
		}
	}
	_, after, err := packageNodes(root, dir)
	if err != nil {
		return "", fmt.Errorf("digest: %w", err)
	}
	if signature != after {
		return "", fmt.Errorf("digest: package changed while reading")
	}
	digest := hex.EncodeToString(h.Sum(nil))
	toolDigests.Lock()
	if len(toolDigests.entries) >= maxCachedToolDigests {
		clear(toolDigests.entries)
	}
	toolDigests.entries[key] = digestRecord{signature: signature, digest: digest}
	toolDigests.Unlock()
	return digest, nil
}
