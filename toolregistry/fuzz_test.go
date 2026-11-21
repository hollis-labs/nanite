package toolregistry

import (
	"archive/tar"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fuzzTar builds a tar from (name, typeflag, body-or-linkname) entries.
func fuzzTar(entries ...[3]string) []byte {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e[0], Typeflag: e[1][0], Mode: 0o644, Size: int64(len(e[2])), Format: tar.FormatPAX}
		if h.Typeflag == tar.TypeDir || h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink {
			h.Size, h.Linkname = 0, e[2]
		}
		_ = w.WriteHeader(h)
		if h.Size > 0 {
			_, _ = w.Write([]byte(e[2]))
		}
	}
	_ = w.Close()
	return buf.Bytes()
}

// FuzzUnpack: an arbitrary archive writes only regular files and directories
// under stage/tools/echo, never a link, never setuid/setgid/sticky bits.
func FuzzUnpack(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 1024))
	f.Add(fuzzTar([3]string{"a", "0", ""}))
	f.Add(fuzzTar([3]string{"a", "0", "x"}, [3]string{"a/", "5", ""}))
	f.Add(fuzzTar([3]string{"a/", "5", ""}, [3]string{"a/b", "0", "x"}))
	f.Add(fuzzTar([3]string{"a", "0", "x"}, [3]string{"a/b", "0", "x"}))
	f.Add(fuzzTar([3]string{"a", "0", "x"}, [3]string{"a", "0", "y"}))
	f.Add(fuzzTar([3]string{"../x", "0", "x"}))
	f.Add(fuzzTar([3]string{"/abs", "0", "x"}))
	f.Add(fuzzTar([3]string{"l", "2", "../../.."}, [3]string{"l/x", "0", "x"}))
	f.Add(fuzzTar([3]string{"h", "1", "../../x"}))
	f.Add(fuzzTar([3]string{"\xff", "0", "x"}))
	f.Add(fuzzTar([3]string{"dir/", "\x00", ""}))
	f.Add(fuzzTar([3]string{"fifo", "6", ""}))
	f.Fuzz(func(t *testing.T, data []byte) {
		base := t.TempDir()
		stage := filepath.Join(base, "stage")
		if err := os.Mkdir(stage, 0o700); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(stage)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()
		_ = unpack(root, "echo", data)
		err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(base, path)
			switch rel {
			case ".", "stage", filepath.Join("stage", "tools"), filepath.Join("stage", "tools", "echo"):
				return nil
			}
			if !strings.HasPrefix(rel, filepath.Join("stage", "tools", "echo")+string(filepath.Separator)) {
				t.Fatalf("wrote outside the package directory: %s", rel)
			}
			if !d.Type().IsRegular() && !d.IsDir() {
				t.Fatalf("link or special file unpacked: %s (%v)", rel, d.Type())
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
				t.Fatalf("privileged mode bits unpacked: %s %v", rel, info.Mode())
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
