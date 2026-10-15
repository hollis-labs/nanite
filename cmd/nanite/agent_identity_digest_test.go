package main

import (
	"io"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func TestDefinitionIdentityDigestSeparatesIdAndName(t *testing.T) {
	a := &agentdef.Definition{DefinitionID: "def:a", Revision: "1", Name: "alpha"}
	b := &agentdef.Definition{DefinitionID: "def:b", Revision: "1", Name: "alpha"}
	c := &agentdef.Definition{DefinitionID: "def:a", Revision: "1", Name: "beta"}
	const same = "sha256:same"
	da, db, dc := definitionIdentityDigest(a, same), definitionIdentityDigest(b, same), definitionIdentityDigest(c, same)
	if da == db || da == dc || db == dc {
		t.Fatalf("identity digests collide: %s %s %s", da, db, dc)
	}
	if da != definitionIdentityDigest(a, same) {
		t.Fatal("identity digest is not stable")
	}
}

func TestAgentHelpIsReachableAndNamesEveryCommand(t *testing.T) {
	var b strings.Builder
	agentUsage(&b)
	for _, want := range []string{"author", "validate", "install", "identity_digest"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("usage lacks %q:\n%s", want, b.String())
		}
	}
	err := runPinnedAgentCommand([]string{"sync"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "nanite agent help") {
		t.Fatalf("unknown command error = %v", err)
	}
}
