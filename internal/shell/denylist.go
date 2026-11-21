package shell

import (
	"path"
	"strings"
)

// CheckDenylist blocks obvious shell accidents before tool authorization. It
// is a convenience, not a security boundary: it parses the command into
// simple commands and argv tokens (honouring quotes, escapes, pipes and
// separators) and matches each command by name and arguments, so an
// argument that merely mentions "reboot" is not refused. It provides no
// filesystem isolation, and a determined command can always evade it.
func CheckDenylist(command string) (blocked bool, reason string) {
	if r := denied(command, 0); r != "" {
		return true, "blocked by denylist: " + r
	}
	return false, ""
}

// denied returns the reason the command is refused, or "".
func denied(command string, depth int) string {
	if strings.Contains(strings.Join(strings.Fields(command), ""), ":(){:|:&};:") {
		return "fork bomb"
	}
	segments := split(command)
	for i, seg := range segments {
		argv := unwrap(seg.argv)
		if r := deniedRedirect(seg.argv); r != "" {
			return r
		}
		if len(argv) == 0 {
			continue
		}
		name := strings.ToLower(path.Base(argv[0]))
		args := argv[1:]
		if r := deniedCommand(name, args); r != "" {
			return r
		}
		if shells[name] {
			// A remote script piped into a shell.
			for j := i - 1; j >= 0 && segments[j+1].piped; j-- {
				if prev := unwrap(segments[j].argv); len(prev) > 0 {
					if fetch := strings.ToLower(path.Base(prev[0])); fetch == "curl" || fetch == "wget" {
						return "pipe remote script to shell"
					}
				}
			}
			// The body of sh -c / bash -c is a command too.
			if depth < 3 {
				for k, arg := range args {
					if arg == "-c" && k+1 < len(args) {
						if r := denied(args[k+1], depth+1); r != "" {
							return r
						}
					}
				}
			}
		}
	}
	return ""
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true}

func deniedCommand(name string, args []string) string {
	switch {
	case name == "rm":
		if recursive(args, "rR") && anyTarget(args, rootOrHome) {
			return "recursive delete of root or home directory"
		}
	case name == "mkfs" || strings.HasPrefix(name, "mkfs."):
		return "filesystem format command"
	case name == "dd":
		for _, a := range args {
			if strings.HasPrefix(a, "of=/dev/") {
				return "raw disk write (dd)"
			}
		}
	case name == "shutdown" || name == "reboot" || name == "halt" || name == "poweroff":
		return "system " + name
	case name == "systemctl":
		for _, a := range args {
			if a == "poweroff" || a == "reboot" || a == "halt" || a == "kexec" {
				return "system " + a
			}
		}
	case name == "init" || name == "telinit":
		if len(args) > 0 && (args[0] == "0" || args[0] == "6") {
			return "system shutdown or reboot (" + name + " " + args[0] + ")"
		}
	case name == "chmod":
		if recursive(args, "R") && anyTarget(args, isRoot) {
			return "recursive permission change on root"
		}
	case name == "chown" || name == "chgrp":
		if recursive(args, "R") {
			return "recursive ownership change"
		}
	case name == "mv":
		if anyTarget(args, isRoot) {
			return "move root filesystem"
		}
	}
	return ""
}

// deniedRedirect refuses output redirected onto a raw disk device.
func deniedRedirect(tokens []string) string {
	for i, t := range tokens {
		if (t == ">" || t == ">>") && i+1 < len(tokens) {
			dev := tokens[i+1]
			for _, prefix := range []string{"/dev/sd", "/dev/disk", "/dev/nvme", "/dev/hd", "/dev/vd", "/dev/xvd", "/dev/mmcblk"} {
				if strings.HasPrefix(dev, prefix) {
					return "raw disk overwrite"
				}
			}
		}
	}
	return ""
}

// recursive reports a short flag containing one of letters, or --recursive,
// before any "--".
func recursive(args []string, letters string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--recursive" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], letters) {
			return true
		}
	}
	return false
}

// anyTarget reports whether an operand (a non-flag argument, or anything
// after "--") satisfies match.
func anyTarget(args []string, match func(string) bool) bool {
	operands := false
	for _, a := range args {
		if !operands && a == "--" {
			operands = true
			continue
		}
		if !operands && strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		if match(a) {
			return true
		}
	}
	return false
}

func isRoot(a string) bool {
	a = strings.TrimRight(a, "/")
	return a == "" || a == "/*" || a == "/." || a == "/.."
}

func rootOrHome(a string) bool {
	if isRoot(a) {
		return true
	}
	for _, home := range []string{"~", "$HOME", "${HOME}"} {
		if rest, ok := strings.CutPrefix(a, home); ok && (rest == "" || strings.Trim(rest, "/*.") == "") {
			return true
		}
	}
	return false
}

// unwrap drops leading environment assignments, reserved words and command
// wrappers (sudo, env, nohup, …) so the real command name comes first.
func unwrap(argv []string) []string {
	for len(argv) > 0 {
		head := argv[0]
		switch {
		case isAssignment(head), head == "{", head == "!", head == "then", head == "do", head == "else", head == "if", head == "while", head == "until":
			argv = argv[1:]
		case head == "sudo" || head == "doas" || head == "nohup" || head == "exec" || head == "command" || head == "builtin" || head == "time" || head == "nice" || head == "env" || head == "stdbuf":
			argv = argv[1:]
			// Wrapper options (with their values) and env's NAME=value operands.
			for len(argv) > 0 && (strings.HasPrefix(argv[0], "-") || isAssignment(argv[0])) {
				if wrapperValueOption[argv[0]] && len(argv) > 1 {
					argv = argv[1:]
				}
				argv = argv[1:]
			}
		default:
			return argv
		}
	}
	return argv
}

// wrapperValueOption lists wrapper options whose value is a separate token
// (sudo -u root, env -u NAME, nice -n 10, …).
var wrapperValueOption = map[string]bool{"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true, "-n": true, "-o": true}

func isAssignment(token string) bool {
	name, _, ok := strings.Cut(token, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

type segment struct {
	argv  []string
	piped bool // this segment reads the previous one through a pipe
}

// split tokenizes a command line into simple commands. Quotes and
// backslashes join into the token they appear in; unquoted ; & | newlines,
// parentheses and backticks end a command; > and >> become their own tokens.
func split(command string) []segment {
	var (
		segments []segment
		argv     []string
		token    strings.Builder
		inToken  bool
		piped    bool
		quote    rune
		escaped  bool
	)
	flushToken := func() {
		if inToken {
			argv = append(argv, token.String())
			token.Reset()
			inToken = false
		}
	}
	flushSegment := func(nextPiped bool) {
		flushToken()
		if len(argv) > 0 {
			segments = append(segments, segment{argv: argv, piped: piped})
		}
		argv, piped = nil, nextPiped
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case escaped:
			if r != '\n' {
				token.WriteRune(r)
				inToken = true
			}
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				token.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(runes) && strings.ContainsRune("\"\\$`", runes[i+1]):
				i++
				token.WriteRune(runes[i])
			default:
				token.WriteRune(r)
			}
		case r == '\\':
			escaped = true
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t':
			flushToken()
		case r == '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
				flushSegment(false)
			} else {
				flushSegment(true)
			}
		case r == ';' || r == '&' || r == '\n' || r == '(' || r == ')' || r == '`':
			flushSegment(false)
		case r == '>':
			flushToken()
			op := ">"
			if i+1 < len(runes) && runes[i+1] == '>' {
				i++
				op = ">>"
			}
			argv = append(argv, op)
		default:
			token.WriteRune(r)
			inToken = true
		}
	}
	flushSegment(false)
	return segments
}
