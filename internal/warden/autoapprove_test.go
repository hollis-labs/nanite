package warden

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestFailFastGrantsFilesWithoutWaiting(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			f, srv := newFake(t)
			var logs bytes.Buffer
			waiting := 0
			s, err := New(Config{URL: srv.URL, CredentialFile: credential(t, "parent-0"), Principal: "agent:test", FailFastGrants: auto, Logger: slog.New(slog.NewTextHandler(&logs, nil)), OnWaiting: func(Waiting) { waiting++ }})
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithConversation(t.Context(), "chat")
			for range 2 {
				_, err = s.Token(ctx, "mcp-gateway", nil)
				if err == nil || !strings.Contains(err.Error(), "mcp-gateway") || !strings.Contains(err.Error(), "req-1") {
					t.Fatalf("error %v", err)
				}
				if _, ok := AsMissingGrant(err); ok == auto {
					t.Fatalf("auto=%v classified waiting=%v", auto, ok)
				}
			}
			if f.requests != 1 {
				t.Fatalf("requests %d", f.requests)
			}
			e := s.entry("mcp-gateway", nil)
			if auto {
				if waiting != 0 || len(e.waiters) != 0 || strings.Count(logs.String(), "grant wait skipped") != 2 {
					t.Fatalf("waiting=%d waiters=%d logs=%s", waiting, len(e.waiters), logs.String())
				}
			} else if waiting != 2 || len(e.waiters) != 1 {
				t.Fatalf("waiting=%d waiters=%d", waiting, len(e.waiters))
			}
		})
	}
}
