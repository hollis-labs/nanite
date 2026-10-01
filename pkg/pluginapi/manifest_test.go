package pluginapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
	"github.com/hollis-labs/plugin-sdk/subprocess"
)

func block() pluginapi.Block {
	return pluginapi.Block{
		UI: pluginapi.UI{Bundle: "ui/index.js"},
		Registers: pluginapi.Registrations{
			Slots:      []pluginapi.Slot{{ID: "bookmarks", Slot: pluginapi.SlotPrimaryDrawer, Component: "BookmarksTab"}},
			Panels:     []pluginapi.Panel{{ID: "bookmark-details", Title: "Bookmark details", Component: "BookmarkPanel"}},
			Envelopes:  []pluginapi.Envelope{{Type: "bookmark", Component: "BookmarkCard", Version: 1, Schema: "schemas/bookmark.json"}},
			Commands:   []pluginapi.Command{{Name: "bookmarks", Description: "Open bookmarks", Aliases: []string{"saved"}}},
			HTTPRoutes: []pluginapi.Route{{Method: "GET", Path: "bookmarks"}},
		},
	}
}

func TestPublicBlockInSharedGeneratedManifest(t *testing.T) {
	raw, err := pluginapi.EncodeBlock(block())
	if err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{SchemaVersion: manifest.SchemaVersion, ID: "hollis.bookmarks", Name: "Bookmarks", Version: "0.1.0", Protocol: subprocess.ProtocolVersion, Runtime: manifest.Runtime,
		Entrypoint: manifest.Entrypoint{Command: "bin/bookmarks"}, Hosts: map[string]manifest.HostRange{"nanite": {Min: pluginapi.Version}}, Nanite: raw}
	var encoded strings.Builder
	if err := manifest.Encode(&encoded, m); err != nil {
		t.Fatal(err)
	}
	decoded, err := manifest.Decode(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := pluginapi.DecodeBlock(decoded.Nanite)
	if err != nil {
		t.Fatal(err)
	}
	if got.Registers.Panels[0].Component != "BookmarkPanel" || got.Registers.Slots[0].Slot != pluginapi.SlotPrimaryDrawer {
		t.Fatal("lost UI declarations")
	}
}

func TestBlockRefusesInvalidRegistrations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*pluginapi.Block)
	}{
		{"escape bundle", func(b *pluginapi.Block) { b.UI.Bundle = "../index.js" }},
		{"Windows bundle", func(b *pluginapi.Block) { b.UI.Bundle = `C:\index.js` }},
		{"no UI bundle", func(b *pluginapi.Block) { b.UI.Bundle = "" }},
		{"missing panel export", func(b *pluginapi.Block) { b.Registers.Panels[0].Component = "" }},
		{"invalid export", func(b *pluginapi.Block) { b.Registers.Slots[0].Component = "a.b" }},
		{"duplicate panel", func(b *pluginapi.Block) { b.Registers.Panels = append(b.Registers.Panels, b.Registers.Panels[0]) }},
		{"schema traversal", func(b *pluginapi.Block) { b.Registers.Envelopes[0].Schema = "../../schema.json" }},
		{"command alias collision", func(b *pluginapi.Block) { b.Registers.Commands[0].Aliases = []string{"bookmarks"} }},
		{"global route", func(b *pluginapi.Block) { b.Registers.HTTPRoutes[0].Path = "/api/tools/call" }},
		{"encoded route escape", func(b *pluginapi.Block) { b.Registers.HTTPRoutes[0].Path = "%2e%2e/core" }},
		{"route traversal", func(b *pluginapi.Block) { b.Registers.HTTPRoutes[0].Path = "../core" }},
		{"load type", func(b *pluginapi.Block) { b.LoadType = "builtin" }},
		{"props type", func(b *pluginapi.Block) { b.Registers.Slots[0].Props = json.RawMessage(`[]`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := block()
			tc.change(&b)
			if b.Validate() == nil {
				t.Fatal("accepted invalid block")
			}
		})
	}
}

func TestDecodeBlockRefusesAmbiguity(t *testing.T) {
	for _, raw := range []string{`null`, `{} {}`, `{"unknown":true}`, `{"UI":{}}`, `{"ui":{"bundle":"one.js","bundle":"two.js"}}`} {
		if _, err := pluginapi.DecodeBlock(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
