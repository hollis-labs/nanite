// Command nanite-agent is the CLI for managing the Nanite agent framework —
// the role/skill/command/template library embedded in the nanite binary and
// installed into ~/.nanite/ and per-project .nanite/ directories.
//
// This is distinct from the `nanite` binary (the chat harness daemon) and
// from the future Nanite desktop installer (a Wails app). Use `nanite-agent`
// to inject or refresh the agent framework in workspaces.
package main

import (
	"fmt"
	"os"

	"github.com/hollis-labs/nanite/internal/cli/installcmd"
	"github.com/hollis-labs/nanite/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		installcmd.Run("nanite-agent init", os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("nanite-agent " + version.Full())
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: nanite-agent <command> [flags]")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  init      install or refresh the Nanite agent framework")
	fmt.Fprintln(os.Stderr, "              --project <dir>   inject into a project's .nanite/")
	fmt.Fprintln(os.Stderr, "              (no --project)    install global ~/.nanite/")
	fmt.Fprintln(os.Stderr, "              --refresh         re-extract embedded assets to ~/.nanite/")
	fmt.Fprintln(os.Stderr, "              --rollback        reverse last migration for --project")
	fmt.Fprintln(os.Stderr, "              --resume|--restart  recover from a partial install")
	fmt.Fprintln(os.Stderr, "              see `nanite-agent init --help` for the full flag list")
	fmt.Fprintln(os.Stderr, "  version   print build version")
}
