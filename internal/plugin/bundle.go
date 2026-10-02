package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/hollis-labs/go-safefs/pathsafe"
)

// ResolveBundleFile checks a declared file against the installed bundle,
// including symlink targets. Executables must carry an execute bit on Unix.
func ResolveBundleFile(root, relative string, executable bool) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("bundle file must be relative")
	}
	resolved, err := pathsafe.ResolveUnder(root, relative)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("bundle path %q must be a regular file", relative)
	}
	if executable && runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("entrypoint %q is not executable", relative)
	}
	return resolved, nil
}
