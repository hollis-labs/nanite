package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestFindCatalogEntry_Match(t *testing.T) {
	y := []byte(`{"schema_version":2,"catalog_version":"0.1.0","generated_at":"2026-10-01T00:00:00Z","plugins":[{"id":"giphy","name":"Giphy","version":"1.0.0","hosts":{"nanite":{"min":"0.1.0"}},"source":{"type":"git","repo":"https://github.com/example/plugins","tag":"v1.0.0"},"archives":[{"platform":"` + runtime.GOOS + "-" + runtime.GOARCH + `","url":"https://example.com/giphy.tar.gz","sha256":"` + strings.Repeat("a", 64) + `","size":123}],"manifest_sha256":"` + strings.Repeat("b", 64) + `","directory":{"status":"active"},"summary":{}}]}`)
	e, ok := findCatalogEntry(y, "giphy")
	if !ok {
		t.Fatal("expected match")
	}
	if e.ArchiveURL != "https://example.com/giphy.tar.gz" {
		t.Errorf("archive_url = %q", e.ArchiveURL)
	}
}

func TestFindCatalogEntry_Miss(t *testing.T) {
	y := []byte("plugins: [{name: other}]")
	if _, ok := findCatalogEntry(y, "giphy"); ok {
		t.Error("expected miss")
	}
}

func TestFindCatalogEntry_BadYAML(t *testing.T) {
	if _, ok := findCatalogEntry([]byte("not: yaml: but: also: valid: maybe"), "x"); ok {
		t.Error("expected miss on malformed yaml")
	}
}

func TestStripSha256Prefix(t *testing.T) {
	cases := map[string]string{
		"sha256:abc":     "abc",
		"abc":            "abc",
		"  sha256:abc  ": "abc",
		"":               "",
	}
	for in, want := range cases {
		if got := stripSha256Prefix(in); got != want {
			t.Errorf("stripSha256Prefix(%q) = %q, want %q", in, got, want)
		}
	}
}
