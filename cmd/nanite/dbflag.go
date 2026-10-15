package main

import "strings"

// cliDB is the value of a -db / --db flag given to an offline or client
// subcommand (auth, agent, mcp, skill, plugin, authority, profile). main
// strips the flag before dispatch and records it here, so resolveDBPathWith
// and resolveAuthPaths see the same explicit database the operator named and
// a subcommand needs no flag plumbing of its own. `serve`, `chat`, `admin`
// and `message` parse their own -db and pass it to those resolvers directly.
var cliDB string

// extractDBFlag removes -db/--db (also -db=X/--db=X) from args wherever it
// appears and returns the last value given. A trailing -db with no value is
// left in place so the subcommand's own parser reports it.
func extractDBFlag(args []string) (string, []string) {
	var db string
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "-db" || a == "--db") && i+1 < len(args):
			db = args[i+1]
			i++
		case strings.HasPrefix(a, "-db=") || strings.HasPrefix(a, "--db="):
			db = a[strings.Index(a, "=")+1:]
		default:
			rest = append(rest, a)
		}
	}
	return db, rest
}
