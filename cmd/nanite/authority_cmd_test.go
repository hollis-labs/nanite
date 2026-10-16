package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAuthorityArgs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      []string
		pos     []string
		reason  string
		wantErr bool
	}{
		{"reason last", []string{"id", "dev_read", "--reason", "workspace helper"}, []string{"id", "dev_read"}, "workspace helper", false},
		{"reason first", []string{"--reason", "r", "id", "a", "b"}, []string{"id", "a", "b"}, "r", false},
		{"equals form", []string{"id", "--reason=because"}, []string{"id"}, "because", false},
		{"no reason", []string{"id", "tool"}, []string{"id", "tool"}, "", false},
		{"blank reason is empty", []string{"id", "--reason", "  "}, []string{"id"}, "", false},
		{"dangling flag", []string{"id", "--reason"}, nil, "", true},
		{"unknown flag", []string{"id", "--granted-by", "someone"}, nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pos, reason, err := parseAuthorityArgs(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && (!reflect.DeepEqual(pos, tc.pos) || reason != tc.reason) {
				t.Fatalf("got %v %q, want %v %q", pos, reason, tc.pos, tc.reason)
			}
		})
	}
}

func TestAuthorityUsageListsTools(t *testing.T) {
	if !strings.Contains(authorityUsage, "tools ") {
		t.Fatal("usage does not list the tools command")
	}
}
