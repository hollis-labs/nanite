package main

import (
	"reflect"
	"testing"
)

func TestExtractLeadingDBFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		db   string
		rest []string
	}{
		{"double dash", []string{"--db", "x.db", "send"}, "x.db", []string{"send"}},
		{"single dash", []string{"-db", "x.db", "inbox"}, "x.db", []string{"inbox"}},
		{"equals", []string{"--db=x.db", "inbox"}, "x.db", []string{"inbox"}},
		{"single dash equals", []string{"-db=x.db", "inbox"}, "x.db", []string{"inbox"}},
		{"not leading is left alone", []string{"send", "--db", "x.db"}, "", []string{"send", "--db", "x.db"}},
		{"none", []string{"inbox"}, "", []string{"inbox"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, rest := extractLeadingDBFlag(tc.in)
			if db != tc.db || !reflect.DeepEqual(rest, tc.rest) {
				t.Fatalf("got %q %v, want %q %v", db, rest, tc.db, tc.rest)
			}
		})
	}
}

func TestExtractURLFlag(t *testing.T) {
	u, rest := extractURLFlag([]string{"--url", "http://h:1", "list"})
	if u != "http://h:1" || !reflect.DeepEqual(rest, []string{"list"}) {
		t.Fatalf("got %q %v", u, rest)
	}
	u, rest = extractURLFlag([]string{"list", "--url=http://h:2"})
	if u != "http://h:2" || !reflect.DeepEqual(rest, []string{"list"}) {
		t.Fatalf("got %q %v", u, rest)
	}
	if u, rest = extractURLFlag([]string{"list"}); u != "" || len(rest) != 1 {
		t.Fatalf("got %q %v", u, rest)
	}
}
