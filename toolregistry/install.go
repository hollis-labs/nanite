package toolregistry

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/hollis-labs/nanite/internal/authored"
)

const lockFile = "tools.lock.json"

type packageLock struct {
	Version  int               `json:"version"`
	Packages map[string]Record `json:"packages"`
}

func readLock(root *os.Root) (packageLock, error) {
	lock := packageLock{Version: 1, Packages: make(map[string]Record)}
	data, err := readFile(root, lockFile, 1<<20)
	if os.IsNotExist(err) {
		return lock, nil
	}
	if err != nil {
		return lock, err
	}
	if err = json.Unmarshal(data, &lock); err != nil {
		return lock, err
	}
	if lock.Version != 1 || lock.Packages == nil {
		return lock, fmt.Errorf("invalid tools lock file")
	}
	for ref, r := range lock.Packages {
		if !matchesRef(r, ref) {
			return lock, fmt.Errorf("invalid locked reference")
		}
		if err := r.validate(); err != nil {
			return lock, err
		}
	}
	return lock, nil
}

// Install resolves a ref, verifies the bytes and pins them in tools.lock.json.
// A changed digest or existing local package is refused, never overwritten.
// The lock is committed before the staged package becomes visible. A crash in
// between can be recovered by repeating the same install. No approval is granted.
func Install(ctx context.Context, client Client, dir, ref string, allowEnv ...string) (Record, error) {
	var r Record
	if client.Records == nil || client.Blobs == nil {
		return r, fmt.Errorf("tool registry is not configured")
	}
	if !validRef(ref) {
		return r, fmt.Errorf("invalid package ref")
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return r, err
	}
	defer func() { _ = root.Close() }()
	guard, err := lockRoot(root)
	if err != nil {
		return r, err
	}
	defer func() { _ = guard.Close() }()
	lock, err := readLock(root)
	if err != nil {
		return r, err
	}
	r, err = client.Records.Resolve(ctx, ref)
	if err != nil {
		return r, err
	}
	if err := r.validate(); err != nil {
		return r, err
	}
	if err := authored.CheckToolName(r.Name); err != nil {
		return r, err
	}
	if !matchesRef(r, ref) {
		return r, fmt.Errorf("registry returned a different reference")
	}
	if prior, ok := lock.Packages[ref]; ok && !reflect.DeepEqual(prior, r) {
		return r, fmt.Errorf("locked reference changed; install refused")
	}
	// Different refs cannot silently take over an installed package name.
	pinned := false
	for _, prior := range lock.Packages {
		if prior.Name == r.Name {
			if prior.Digest != r.Digest {
				return r, fmt.Errorf("package name already pinned to another digest")
			}
			pinned = true
		}
	}
	if err := requireDirectory(root, "tools"); err != nil {
		return r, err
	}
	if _, err := root.Lstat("tools/" + r.Name); err == nil {
		if !pinned {
			return r, fmt.Errorf("local package already exists; refusing to overwrite")
		}
		if err := verify(dir, r, allowEnv); err != nil {
			return r, err
		}
		lock.Packages[ref] = r
		return r, writeLock(root, lock)
	} else if !os.IsNotExist(err) {
		return r, err
	}
	data, err := client.Blobs.Fetch(ctx, r.Artifact)
	if err != nil {
		return r, err
	}
	if len(data) > maxBundleBytes {
		return r, fmt.Errorf("artifact too large")
	}
	if artifactRef(data) != r.Artifact {
		return r, fmt.Errorf("artifact SHA-256 mismatch")
	}
	stage := ".nanite-install-" + rand.Text()
	if err := root.Mkdir(stage, 0700); err != nil {
		return r, err
	}
	defer func() { _ = root.RemoveAll(stage) }()
	staged, err := root.OpenRoot(stage)
	if err != nil {
		return r, err
	}
	defer func() { _ = staged.Close() }()
	if err := unpack(staged, r.Name, data); err != nil {
		return r, err
	}
	if err := verify(filepath.Join(dir, stage), r, allowEnv); err != nil {
		return r, err
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	lock.Packages[ref] = r
	if err := writeLock(root, lock); err != nil {
		return r, err
	}
	if _, err := root.Lstat("tools/" + r.Name); err == nil {
		return r, fmt.Errorf("package appeared during install")
	} else if !os.IsNotExist(err) {
		return r, err
	}
	if err := root.Rename(stage+"/tools/"+r.Name, "tools/"+r.Name); err != nil {
		return r, err
	}
	toolsDir, err := root.Open("tools")
	if err != nil {
		return r, err
	}
	defer func() { _ = toolsDir.Close() }()
	return r, toolsDir.Sync()
}

func writeLock(root *os.Root, lock packageLock) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("tools lock file too large")
	}
	return atomicWrite(root, lockFile, append(data, '\n'))
}
