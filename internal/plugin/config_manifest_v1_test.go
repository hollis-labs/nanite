package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPluginManifestV1Roundtrip exercises every v1 field so additions to the
// schema are covered by a single authoritative fixture.
func TestPluginManifestV1Roundtrip(t *testing.T) {
	src := `schema_version: 1
name: Giphy
id: giphy
version: 1.2.3
description: Giphy integration
author: Acme
url: https://giphy.example
short_desc: gif search
license: MIT
homepage: https://giphy.example/home
repository: https://github.com/acme/giphy
protocol: 1
nanite_compat:
  min: "0.9.0"
  max: "1.999.0"
config:
  api_key:
    type: secret
    required: true
    env_var: GIPHY_API_KEY
    description: giphy api key
dependencies:
  - other-plugin
requires:
  mcp_servers: [memory]
  plugins: [oembed]
  features: [chat]
registers:
  envelopes:
    - type: giphy-modal
      component: GiphyModalCard
      version: 1
      schema: envelopes/giphy-modal.schema.json
  commands:
    - name: giphy
      description: Search giphy
      args:
        - name: q
          type: string
          required: true
          description: query
      aliases: [gif]
      hidden: false
      handler: search
      metadata:
        rate_limit: "5/min"
  slots:
    - slot: composer-toolbar
      id: giphy-quick-action
      component: GiphyToolbar
      priority: 10
      props:
        icon: gif
  components:
    - name: GiphyModalCard
      type: envelope
      description: Renders giphy modal
      export: GiphyModalCard
  keybindings:
    - id: giphy-open
      keys: "cmd+g"
      description: open giphy
      command: giphy
      when: chat-focused
  events:
    - types: [message.created]
      handler: on_message
      priority: 1
  crud:
    - resource: gif
      methods: [read]
  http_routes:
    - pattern: /webhook
      method: POST
      handler: webhook
  mcp_servers:
    - name: giphy
      description: giphy mcp server
      tools: [search]
  agent_profiles:
    - id: giphy-agent
      file: agents/giphy.yaml
ui:
  bundle_dir: ui/dist
  entry: index.js
  stylesheet: style.css
  assets_dir: assets
  react_version: ^19.0.0
release:
  archive_url: https://example.com/a.tar.gz
  checksum_url: https://example.com/a.sha256
  signature_url: https://example.com/a.sig
  platforms: [darwin-arm64, linux-amd64]
runtime: subprocess
entrypoint: ./giphy
load_type: auto
tool_overrides:
  search:
    load_type: opt-in
`

	var m PluginManifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m.SchemaVersion != 1 {
		t.Errorf("schema_version: got %d", m.SchemaVersion)
	}
	if m.ID != "giphy" || m.Name != "Giphy" {
		t.Errorf("identity mismatch: id=%q name=%q", m.ID, m.Name)
	}
	if m.License != "MIT" || m.Homepage == "" || m.Repository == "" {
		t.Errorf("license/homepage/repository missing")
	}
	if m.Protocol != 1 {
		t.Errorf("protocol: got %d", m.Protocol)
	}
	if m.NaniteCompat.Min != "0.9.0" || m.NaniteCompat.Max != "1.999.0" {
		t.Errorf("nanite_compat: %+v", m.NaniteCompat)
	}
	if len(m.Requires.McpServers) != 1 || m.Requires.McpServers[0] != "memory" {
		t.Errorf("requires.mcp_servers: %+v", m.Requires.McpServers)
	}
	if len(m.Requires.Plugins) != 1 || len(m.Requires.Features) != 1 {
		t.Errorf("requires plugins/features: %+v", m.Requires)
	}

	r := m.Registers
	if len(r.Envelopes) != 1 || r.Envelopes[0].Type != "giphy-modal" || r.Envelopes[0].Component != "GiphyModalCard" || r.Envelopes[0].Version != 1 {
		t.Errorf("envelopes: %+v", r.Envelopes)
	}
	if len(r.Commands) != 1 || r.Commands[0].Name != "giphy" || len(r.Commands[0].Args) != 1 || !r.Commands[0].Args[0].Required {
		t.Errorf("commands: %+v", r.Commands)
	}
	if len(r.Slots) != 1 || r.Slots[0].Slot != "composer-toolbar" || r.Slots[0].Priority != 10 {
		t.Errorf("slots: %+v", r.Slots)
	}
	if len(r.Components) != 1 || r.Components[0].Name != "GiphyModalCard" {
		t.Errorf("components: %+v", r.Components)
	}
	if len(r.Keybindings) != 1 || r.Keybindings[0].Keys != "cmd+g" {
		t.Errorf("keybindings: %+v", r.Keybindings)
	}
	if len(r.Events) != 1 || len(r.Events[0].Types) != 1 {
		t.Errorf("events: %+v", r.Events)
	}
	if len(r.Crud) != 1 || r.Crud[0].Resource != "gif" {
		t.Errorf("crud: %+v", r.Crud)
	}
	if len(r.HttpRoutes) != 1 || r.HttpRoutes[0].Method != "POST" {
		t.Errorf("http_routes: %+v", r.HttpRoutes)
	}
	if len(r.McpServers) != 1 || r.McpServers[0].Name != "giphy" {
		t.Errorf("mcp_servers: %+v", r.McpServers)
	}
	if len(r.AgentProfiles) != 1 || r.AgentProfiles[0].File != "agents/giphy.yaml" {
		t.Errorf("agent_profiles: %+v", r.AgentProfiles)
	}

	if m.UI.BundleDir != "ui/dist" || m.UI.ReactVersion != "^19.0.0" {
		t.Errorf("ui: %+v", m.UI)
	}
	if m.Release.ArchiveURL == "" || len(m.Release.Platforms) != 2 {
		t.Errorf("release: %+v", m.Release)
	}

	// Legacy fields still parse.
	if m.Runtime != "subprocess" || m.Entrypoint != "./giphy" {
		t.Errorf("runtime/entrypoint: %q/%q", m.Runtime, m.Entrypoint)
	}
	if m.LoadType != LoadTypeAuto {
		t.Errorf("load_type: %q", m.LoadType)
	}
	if _, ok := m.ToolOverrides["search"]; !ok {
		t.Errorf("tool_overrides: %+v", m.ToolOverrides)
	}
	if _, ok := m.Config["api_key"]; !ok {
		t.Errorf("config: %+v", m.Config)
	}
	if len(m.Dependencies) != 1 {
		t.Errorf("dependencies: %+v", m.Dependencies)
	}
}

// TestPluginManifestLegacyParse verifies that a pre-v1 manifest without any of
// the new fields still parses cleanly (no regressions).
func TestPluginManifestLegacyParse(t *testing.T) {
	src := `name: oldplug
version: 0.1.0
description: legacy
author: legacy
config:
  token:
    type: string
    required: false
`
	var m PluginManifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if m.Name != "oldplug" || m.SchemaVersion != 0 {
		t.Errorf("legacy parse: %+v", m)
	}
	if _, ok := m.Config["token"]; !ok {
		t.Errorf("legacy config missing: %+v", m.Config)
	}
	// Structured zero values
	if len(m.Registers.Envelopes) != 0 || len(m.Requires.Plugins) != 0 {
		t.Errorf("zero-valued structs not empty: %+v", m)
	}
}

// TestParseManifestV1File exercises ParseManifest against a real file.
func TestParseManifestV1File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(path, []byte("schema_version: 1\nid: foo\nname: Foo\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.SchemaVersion != 1 || m.ID != "foo" {
		t.Errorf("parsed: %+v", m)
	}
}
