package toolregistry

import (
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

func readFile(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid or oversized registry file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("registry file exceeds size limit")
	}
	return data, err
}

func atomicWrite(root *os.Root, name string, data []byte) error {
	tmp := ".registry-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmp) }()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	errClose := f.Close()
	if err != nil {
		return err
	}
	if errClose != nil {
		return errClose
	}
	if err = root.Rename(tmp, name); err != nil {
		return err
	}
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func lockRoot(root *os.Root) (*os.File, error) {
	f, err := root.OpenFile(".registry.lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("registry lock must be a regular file")
	}
	if err == nil {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	} // #nosec G115 -- Fd is a Unix file descriptor.
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("registry operation busy or lock unavailable: %w", err)
	}
	return f, nil
}

func requireDirectory(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return root.Mkdir(name, 0755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a directory, not a symlink", name)
	}
	return nil
}
