package lintprobe

import "os"

// unusedHelper is never referenced anywhere — the `unused` linter flags it.
func unusedHelper() int { return 1 }

// Probe drops an error return on the floor — errcheck flags it.
func Probe() {
	os.Remove("/tmp/nanite-lintprobe-does-not-exist")
}
