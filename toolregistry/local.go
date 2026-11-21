package toolregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// Local is an explicitly configured filesystem fake implementing Registry and
// BlobStore separately. It has no authentication and is not a shared service.
type Local struct{ Dir string }

// LocalClient connects development-only record and blob fakes in one directory.
func LocalClient(dir string) Client {
	local := Local{Dir: dir}
	return Client{Records: local, Blobs: local}
}

func (l Local) open() (*os.Root, error) {
	if l.Dir == "" {
		return nil, fmt.Errorf("local registry directory is required")
	}
	if err := os.MkdirAll(l.Dir, 0700); err != nil {
		return nil, err
	}
	return os.OpenRoot(l.Dir)
}
func refFile(ref string) string {
	h := sha256.Sum256([]byte(ref))
	return "ref-" + hex.EncodeToString(h[:]) + ".json"
}

// Publish saves a record with an immutable version-to-digest mapping.
func (l Local) Publish(ctx context.Context, r Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.validate(); err != nil {
		return err
	}
	root, err := l.open()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	lock, err := lockRoot(ctx, root)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	raw, err := readFile(root, refFile(r.Ref()), 1<<20)
	if err == nil {
		var old Record
		if json.Unmarshal(raw, &old) != nil || !reflect.DeepEqual(old, r) {
			return fmt.Errorf("registry reference already has different content: %s", r.Ref())
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	raw, err = json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicWrite(root, refFile(r.Ref()), raw)
}

// Resolve reads the immutable record with the requested ID.
func (l Local) Resolve(ctx context.Context, ref string) (Record, error) {
	var r Record
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if !validRef(ref) {
		return r, fmt.Errorf("invalid package ref")
	}
	root, err := l.open()
	if err != nil {
		return r, err
	}
	defer func() { _ = root.Close() }()
	raw, err := readFile(root, refFile(ref), 1<<20)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if err = r.validate(); err != nil {
		return r, err
	}
	if !matchesRef(r, ref) {
		return r, fmt.Errorf("registry returned a different reference")
	}
	return r, nil
}

// Put uploads artifact bytes by their SHA-256 content address.
func (l Local) Put(ctx context.Context, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > maxBundleBytes {
		return "", fmt.Errorf("artifact too large")
	}
	ref := artifactRef(data)
	root, err := l.open()
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	lock, err := lockRoot(ctx, root)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Close() }()
	name := strings.TrimPrefix(ref, "cas://sha256/") + ".tar"
	if existing, err := readFile(root, name, maxBundleBytes); err == nil {
		if !bytes.Equal(existing, data) {
			return "", fmt.Errorf("content address has different bytes")
		}
		return ref, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return ref, atomicWrite(root, name, data)
}

// Fetch reads an artifact by exact CAS address. Install verifies its hash.
func (l Local) Fetch(ctx context.Context, artifact string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !artifactPattern.MatchString(artifact) {
		return nil, fmt.Errorf("invalid artifact address")
	}
	root, err := l.open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return readFile(root, strings.TrimPrefix(artifact, "cas://sha256/")+".tar", maxBundleBytes)
}
