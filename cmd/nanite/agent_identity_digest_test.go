package main

import (
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
