package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const authorityUsage = `Usage: nanite authority <command>

Reviewed tool grants for native views (operator credential required). A grant
applies to one reviewed host settings revision; changing the settings means a
new review. Every change needs --reason and is recorded with your credential.

  tools                                         tool names a grant can name (underscored, e.g. dev_read)
  list <host-settings-id>                       authorities and their tools
  show <authority-id>
  create <host-settings-id> --reason R          review the host settings now in force
  revoke <authority-id> --reason R              end an authority (final)
  grant <authority-id> <tool>... --reason R     grant available tools (see "tools"; dev.read is read as dev_read)
  ungrant <authority-id> <tool>... --reason R   remove grants`

// parseAuthorityArgs separates positionals from --reason wherever it appears.
func parseAuthorityArgs(args []string) (positional []string, reason string, err error) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--reason" || a == "-reason":
			if i+1 >= len(args) {
				return nil, "", errors.New("--reason needs a value")
			}
			i++
			reason = args[i]
		case strings.HasPrefix(a, "--reason="):
			reason = strings.TrimPrefix(a, "--reason=")
		case strings.HasPrefix(a, "-"):
			return nil, "", fmt.Errorf("unknown flag %q", a)
		default:
			positional = append(positional, a)
		}
	}
	return positional, strings.TrimSpace(reason), nil
}

func cmdAuthority(args []string) {
	fail := func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(2)
	}
	if len(args) == 0 {
		fail(authorityUsage)
	}
	pos, reason, err := parseAuthorityArgs(args[1:])
	if err != nil {
		fail(err.Error())
	}
	needReason := func() {
		if reason == "" {
			fail("--reason is required")
		}
	}
	switch args[0] {
	case "tools":
		if len(pos) != 0 {
			fail("usage: nanite authority tools")
		}
		err = authCall(http.MethodGet, "/api/native-authority/tools", nil)
	case "list":
		if len(pos) != 1 {
			fail("usage: nanite authority list <host-settings-id>")
		}
		err = authCall(http.MethodGet, "/api/native-authority?host_settings_id="+url.QueryEscape(pos[0]), nil)
	case "show":
		if len(pos) != 1 {
			fail("usage: nanite authority show <authority-id>")
		}
		err = authCall(http.MethodGet, "/api/native-authority/"+url.PathEscape(pos[0]), nil)
	case "create":
		if len(pos) != 1 {
			fail("usage: nanite authority create <host-settings-id> --reason R")
		}
		needReason()
		err = authCall(http.MethodPost, "/api/native-authority", map[string]string{"host_settings_id": pos[0], "reason": reason})
	case "revoke":
		if len(pos) != 1 {
			fail("usage: nanite authority revoke <authority-id> --reason R")
		}
		needReason()
		err = authCall(http.MethodPost, "/api/native-authority/"+url.PathEscape(pos[0])+"/revoke", map[string]string{"reason": reason})
	case "grant", "ungrant":
		if len(pos) < 2 {
			fail("usage: nanite authority " + args[0] + " <authority-id> <tool>... --reason R")
		}
		needReason()
		method := http.MethodPut
		if args[0] == "ungrant" {
			method = http.MethodDelete
		}
		for _, tool := range pos[1:] {
			if err = authCall(method, "/api/native-authority/"+url.PathEscape(pos[0])+"/tools/"+url.PathEscape(tool), map[string]string{"reason": reason}); err != nil {
				break
			}
		}
	default:
		fail(authorityUsage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
