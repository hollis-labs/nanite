package toolregistry

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/hollis-labs/nanite/internal/authored"
)

const maxBundleBytes = 72 << 20
const maxEntries = 4096

func recordFor(pkg authored.ToolPackage, publisher string) Record {
	id, _ := publisherID(publisher)
	r := Record{ID: "tool-package:" + id + "/" + pkg.Name + "@" + pkg.Version, Kind: "tool-package/1", Name: pkg.Name, Version: pkg.Version, Digest: "sha256:" + pkg.Digest, Entry: pkg.Entry, Execution: pkg.Kind, Env: pkg.Env, Provenance: Provenance{Publisher: publisher}}
	for _, n := range pkg.Needs {
		r.Needs = append(r.Needs, Need{Audience: n.Audience, Scopes: n.Scopes, Env: n.Env})
	}
	return r
}

func artifactRef(data []byte) string {
	h := sha256.Sum256(data)
	return "cas://sha256/" + hex.EncodeToString(h[:])
}

func capture(root *os.Root, name string) ([]byte, error) {
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	remaining := int64(authored.MaxToolPackageBytes)
	base := "tools/" + name
	var paths []string
	err := fs.WalkDir(root.FS(), base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == base {
			return nil
		}
		if len(paths) >= maxEntries {
			return fmt.Errorf("too many package entries")
		}
		if d.IsDir() {
			p += "/"
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	for _, entryPath := range paths {
		p := strings.TrimSuffix(entryPath, "/")
		info, err := root.Lstat(p)
		if err != nil {
			return nil, err
		}
		header := &tar.Header{Name: strings.TrimPrefix(p, base+"/"), Mode: int64(info.Mode().Perm()), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		var data []byte
		if info.IsDir() {
			header.Typeflag = tar.TypeDir
			header.Mode = 0755
			header.Name += "/"
		} else {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("registry packages cannot contain links or special files")
			}
			if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
				return nil, fmt.Errorf("registry packages cannot contain hard links")
			}
			data, err = readFile(root, p, remaining)
			if err != nil {
				return nil, err
			}
			remaining -= int64(len(data))
			header.Size = int64(len(data))
		}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err = writer.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > maxBundleBytes {
		return nil, fmt.Errorf("artifact too large")
	}
	return buf.Bytes(), nil
}

// unpack writes only inside a private staging directory, never the live tools tree.
func unpack(root *os.Root, name string, data []byte) error {
	if len(data) > maxBundleBytes {
		return fmt.Errorf("artifact too large")
	}
	if err := root.MkdirAll("tools/"+name, 0755); err != nil {
		return err
	}
	reader := tar.NewReader(bytes.NewReader(data))
	seen := make(map[string]bool)
	total := int64(0)
	for {
		h, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		relative := strings.TrimSuffix(h.Name, "/")
		if !fs.ValidPath(relative) || relative == "." || strings.ContainsAny(relative, "\\\x00") || len(relative) > 4096 || seen[relative] || h.Mode < 0 || h.Mode > 0777 || len(seen) >= maxEntries {
			return fmt.Errorf("invalid or duplicate package path or mode")
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return fmt.Errorf("artifact contains a link or special file")
		}
		if h.Size < 0 || h.Size > int64(authored.MaxToolPackageBytes)-total {
			return fmt.Errorf("package exceeds byte limit")
		}
		total += h.Size
		seen[relative] = true
		target := "tools/" + name + "/" + relative
		if err := root.MkdirAll(path.Dir(target), 0755); err != nil {
			return err
		}
		if h.Typeflag == tar.TypeDir {
			if h.Size != 0 {
				return fmt.Errorf("directory contains data")
			}
			if err := root.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		out, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = io.CopyN(out, reader, h.Size)
		if err == nil {
			err = out.Chmod(fs.FileMode(h.Mode))
		}
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if len(seen) == 0 {
		return fmt.Errorf("empty package artifact")
	}
	return nil
}

func verify(dir string, r Record, allowEnv []string) error {
	pkg, err := authored.LoadTool(dir, r.Name, allowEnv...)
	if err != nil {
		return err
	}
	expected := recordFor(pkg, r.Provenance.Publisher)
	expected.Provenance = r.Provenance
	expected.Artifact = r.Artifact
	if !reflect.DeepEqual(expected, r) {
		return fmt.Errorf("package digest or metadata does not match registry record")
	}
	return nil
}

// Publish validates and snapshots a local package into the agent's own identity
// namespace. It never executes the package or promotes it to a shared namespace.
func Publish(ctx context.Context, client Client, dir, name, publisher string, allowEnv ...string) (Record, error) {
	var r Record
	if client.Records == nil || client.Blobs == nil {
		return r, fmt.Errorf("tool registry is not configured")
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if err := authored.CheckToolName(name); err != nil {
		return r, err
	}
	if _, err := publisherID(publisher); err != nil {
		return r, err
	}
	pkg, err := authored.LoadTool(dir, name, allowEnv...)
	if err != nil {
		return r, err
	}
	r = recordFor(pkg, publisher)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return r, err
	}
	defer func() { _ = root.Close() }()
	data, err := capture(root, name)
	if err != nil {
		return r, err
	}
	r.Artifact = artifactRef(data)
	if err := r.validate(); err != nil {
		return r, err
	}
	stage, err := os.MkdirTemp("", "nanite-publish-")
	if err != nil {
		return r, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	staged, err := os.OpenRoot(stage)
	if err != nil {
		return r, err
	}
	defer func() { _ = staged.Close() }()
	if err := unpack(staged, name, data); err != nil {
		return r, err
	}
	if err := verify(stage, r, allowEnv); err != nil {
		return r, err
	}
	artifact, err := client.Blobs.Put(ctx, data)
	if err != nil {
		return r, err
	}
	if artifact != r.Artifact {
		return r, fmt.Errorf("blob store returned a different content address")
	}
	if err := client.Records.Publish(ctx, r); err != nil {
		return r, err
	}
	return r, nil
}
