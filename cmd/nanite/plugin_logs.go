package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/brand"
)

// pluginLogs tails the stderr log of a subprocess plugin. Logs are written
// by the subprocess manager under ~/.<brand>/plugin-logs/<id>.stderr.log.
// This command does not follow — it prints the current contents and exits.
func pluginLogs(id string) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve home dir: %v\n", err)
		os.Exit(1)
	}
	logPath := filepath.Join(home, "."+brand.BinaryName, "plugin-logs", id+".stderr.log")
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "No log file found for %q at %s\n", id, logPath)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "open log: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	if _, err := io.Copy(os.Stdout, f); err != nil {
		fmt.Fprintf(os.Stderr, "read log: %v\n", err)
		os.Exit(1)
	}
}
