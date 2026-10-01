package main

import (
	"testing"
)

func TestFindCatalogEntry_Match(t *testing.T) {
	y := []byte(`
plugins:
  - name: giphy
    archive_url: https://example.com/giphy.tar.gz
    checksum: sha256:deadbeef
  - name: other
    archive_url: https://example.com/other.tar.gz
`)
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
