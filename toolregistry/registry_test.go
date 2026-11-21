package toolregistry

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/authored"
)

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll("tools/echo/a", 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"identity.yaml": "principal: agent:fixture\n", "tools/echo/tool.yaml": "name: echo\ndescription: Echo input\nversion: 1.0.0\nkind: command\nentry: run.sh\n", "tools/echo/a-": "sorting fixture", "tools/echo/a/x": "nested fixture", "tools/echo/run.sh": "#!/bin/sh\ncat\n"} {
		mode := os.FileMode(0600)
		if strings.HasSuffix(name, ".sh") {
			mode = 0700
		}
		if err := root.WriteFile(name, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func publishFixture(t *testing.T) (string, Client, Record) {
	t.Helper()
	dir := fixture(t)
	local := LocalClient(t.TempDir())
	r, err := Publish(t.Context(), local, dir, "echo", "msg://agent/agent-mux/agt_fixture")
	if err != nil {
		t.Fatal(err)
	}
	return dir, local, r
}

func TestPublishInstallPinnedRoundTrip(t *testing.T) {
	src, local, r := publishFixture(t)
	target := t.TempDir()
	if r.ID != "tool-package:agt_fixture/echo@1.0.0" || r.Provenance.Publisher != "msg://agent/agent-mux/agt_fixture" {
		t.Fatalf("namespace %+v", r)
	}
	for range 2 {
		if _, err := Install(t.Context(), local, target, r.Ref()); err != nil {
			t.Fatal(err)
		}
	}
	original, err := authored.LoadTool(src, "echo")
	if err != nil {
		t.Fatal(err)
	}
	installed, err := authored.LoadTool(target, "echo")
	if err != nil {
		t.Fatal(err)
	}
	if original.Digest != installed.Digest {
		t.Fatalf("different content: %s vs %s", original.Digest, installed.Digest)
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	lock, err := readLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Packages[r.Ref()].Digest != r.Digest {
		t.Fatalf("missing pin %+v", lock)
	}
	// Recover a lock-only interrupted install by fetching the same pinned content.
	if err := root.RemoveAll("tools/echo"); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(t.Context(), local, target, r.Ref()); err != nil {
		t.Fatal(err)
	}
	if err := verify(target, r, nil); err != nil {
		t.Fatal(err)
	}

}

type changedRegistry struct {
	Registry
	BlobStore
	record Record
	data   []byte
}

func (c changedRegistry) Resolve(context.Context, string) (Record, error) { return c.record, nil }
func (c changedRegistry) Fetch(ctx context.Context, digest string) ([]byte, error) {
	if c.data != nil {
		return c.data, nil
	}
	return c.BlobStore.Fetch(ctx, digest)
}

func TestInstallRejectsChangedPinAndCorruptBytes(t *testing.T) {
	_, local, r := publishFixture(t)
	target := t.TempDir()
	if _, err := Install(t.Context(), local, target, r.Ref()); err != nil {
		t.Fatal(err)
	}
	changed := r
	changed.Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := Install(t.Context(), Client{Records: changedRegistry{Registry: local.Records, record: changed}, Blobs: local.Blobs}, target, r.Ref()); err == nil {
		t.Fatal("changed pin accepted")
	}
	data, err := local.Blobs.Fetch(t.Context(), r.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	bad := mutateTar(t, data, func(h *tar.Header, data []byte) []byte {
		if h.Name == "run.sh" {
			return []byte("#!/bin/sh\necho changed\n")
		}
		return data
	})
	target = t.TempDir()
	if _, err := Install(t.Context(), Client{Records: local.Records, Blobs: changedRegistry{BlobStore: local.Blobs, data: bad}}, target, r.Ref()); err == nil {
		t.Fatal("corrupt artifact accepted")
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if _, err := root.Stat("tools/echo"); !os.IsNotExist(err) {
		t.Fatal("failed install exposed package")
	}
	if _, err := root.Stat(lockFile); !os.IsNotExist(err) {
		t.Fatal("failed verification wrote pin")
	}
}

func TestRegistryRefusesOverwriteAndMutableVersions(t *testing.T) {
	src, local, r := publishFixture(t)
	if _, err := Install(t.Context(), local, src, r.Ref()); err == nil {
		t.Fatal("untracked local package overwritten")
	}
	root, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile("tools/echo/run.sh", []byte("#!/bin/sh\necho changed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(t.Context(), local, src, "echo", "msg://agent/agent-mux/agt_fixture"); err == nil {
		t.Fatal("mutable version allowed")
	}
	target := t.TempDir()
	if _, err := Install(t.Context(), local, target, r.Ref()); err != nil {
		t.Fatal(err)
	}
	installed, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = installed.Close() }()
	if err := installed.WriteFile("tools/echo/run.sh", []byte("#!/bin/sh\necho local\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(t.Context(), local, target, r.Ref()); err == nil {
		t.Fatal("local modification silently overwritten")
	}
}

func TestBundlePathsAndLinks(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "a/../../outside", "a\\outside", ".", "a//b"} {
		t.Run(name, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			raw := oneTar(t, &tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg}, []byte("bad"))
			if err := unpack(root, "echo", raw); err == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	for _, hard := range []bool{false, true} {
		dir := fixture(t)
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		if hard {
			err = root.Link("tools/echo/run.sh", "tools/echo/linked")
		} else {
			err = root.Symlink("run.sh", "tools/echo/linked")
		}
		_ = root.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Publish(t.Context(), LocalClient(t.TempDir()), dir, "echo", "msg://agent/agent-mux/agt_fixture"); err == nil {
			t.Fatalf("link accepted hard=%v", hard)
		}
	}
}

func TestInstallRefusesSymlinkToolsOrLock(t *testing.T) {
	_, local, r := publishFixture(t)
	for _, name := range []string{"tools", lockFile} {
		target := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(target, name)); err != nil {
			t.Fatal(err)
		}
		if _, err := Install(t.Context(), local, target, r.Ref()); err == nil {
			t.Fatalf("symlink %s accepted", name)
		}
		entries, err := os.ReadDir(outside)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatal("wrote outside agent")
		}
	}
}

func TestInstallLockSerializesWriters(t *testing.T) {
	_, local, r := publishFixture(t)
	target := t.TempDir()
	root, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	guard, err := lockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guard.Close() }()
	if _, err := Install(t.Context(), local, target, r.Ref()); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("concurrent install: %v", err)
	}
}

func oneTar(t *testing.T, h *tar.Header, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	h.Size = int64(len(data))
	if err := w.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func mutateTar(t *testing.T, raw []byte, change func(*tar.Header, []byte) []byte) []byte {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(raw))
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for {
		h, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		data = change(h, data)
		h.Size = int64(len(data))
		if err := writer.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTarDeterminismAndBothDigests(t *testing.T) {
	src, client, r := publishFixture(t)
	before, err := client.Blobs.Fetch(t.Context(), r.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.Chtimes("tools/echo/run.sh", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	after, err := capture(root, "echo")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("tar changed after timestamp-only change")
	}
	reader := tar.NewReader(bytes.NewReader(after))
	last := ""
	for {
		h, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name < last {
			t.Fatal("tar entries not sorted")
		}
		last = h.Name
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || !h.ModTime.Equal(time.Unix(0, 0)) {
			t.Fatalf("noncanonical tar metadata %+v", h)
		}
	}
	changed := mutateTar(t, before, func(h *tar.Header, b []byte) []byte {
		if h.Name == "run.sh" {
			return []byte("#!/bin/sh\necho bad\n")
		}
		return b
	})
	// Even a correct CAS hash cannot bypass the separate whole-package digest.
	r.Artifact = artifactRef(changed)
	malicious := Client{Records: changedRegistry{Registry: client.Records, record: r}, Blobs: changedRegistry{BlobStore: client.Blobs, data: changed}}
	if _, err := Install(t.Context(), malicious, t.TempDir(), r.Ref()); err == nil || !strings.Contains(err.Error(), "digest or metadata") {
		t.Fatalf("package digest check: %v", err)
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeFifo} {
		raw := oneTar(t, &tar.Header{Name: "linked", Typeflag: kind, Linkname: "outside", Mode: 0600}, nil)
		stage, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		err = unpack(stage, "echo", raw)
		_ = stage.Close()
		if err == nil {
			t.Fatalf("tar link/special type %c accepted", kind)
		}
	}
}

func TestPublishRequiresOpaquePublisher(t *testing.T) {
	for _, urn := range []string{"", "agent:fixture", "msg://agent/agent-mux/display-name", "msg://user/agent-mux/agt_fixture"} {
		if _, err := Publish(t.Context(), LocalClient(t.TempDir()), fixture(t), "echo", urn); err == nil {
			t.Fatalf("publisher accepted: %q", urn)
		}
	}
}
