package agent

import "os"

// osStatNoErr is a tiny shim around os.Stat that lets tests assert
// "directory removed" without importing os everywhere.
func osStatNoErr(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
