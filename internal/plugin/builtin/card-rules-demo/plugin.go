// Package card_rules_demo is the reference plugin for the J5 card_rules
// manifest extension (CW-20260421-0013). It registers two Stage 1 card
// detection rules via plugin.yaml:
//
//   - metrics-summary: regex rule that triggers on ```metrics fenced blocks.
//   - data-table:      regex rule that triggers on ```data-table fenced blocks.
//
// This plugin is intentionally minimal — it demonstrates the card_rules
// manifest section and the host registration path without shipping any
// frontend components. Real plugins pairing card_rules with envelope rendering
// would also declare matching entries under registers.envelopes.
package cardrulesdemo

import (
	_ "embed"
	"sync"
	"time"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/plugin-sdk"
	"gopkg.in/yaml.v3"
)

//go:embed plugin.yaml
var manifestYAML []byte

var (
	parsedManifestOnce sync.Once
	parsedManifest     *hostplugin.PluginManifest
)

func loadManifest() *hostplugin.PluginManifest {
	parsedManifestOnce.Do(func() {
		var m hostplugin.PluginManifest
		if err := yaml.Unmarshal(manifestYAML, &m); err != nil {
			panic("card-rules-demo: invalid embedded plugin.yaml: " + err.Error())
		}
		parsedManifest = &m
	})
	return parsedManifest
}

func init() {
	hostplugin.RegisterPlugin("card-rules-demo", func() plugin.Plugin { return New() })
}

// CardRulesDemoPlugin is the reference implementation for J5 card_rules.
type CardRulesDemoPlugin struct {
	status plugin.PluginStatus
}

// New returns a new CardRulesDemoPlugin.
func New() *CardRulesDemoPlugin { return &CardRulesDemoPlugin{} }

func (p *CardRulesDemoPlugin) ID() string      { return "card-rules-demo" }
func (p *CardRulesDemoPlugin) Name() string    { return "Card Rules Demo" }
func (p *CardRulesDemoPlugin) Version() string { return "1.0.0" }
func (p *CardRulesDemoPlugin) Description() string {
	return "Reference plugin demonstrating card_rules manifest extension (J5)"
}
func (p *CardRulesDemoPlugin) Dependencies() []string { return nil }

// Manifest exposes the embedded plugin.yaml so card_rules register through
// the host's yaml-authoritative loader path (applyManifestRegistrations).
func (p *CardRulesDemoPlugin) Manifest() *hostplugin.PluginManifest { return loadManifest() }

func (p *CardRulesDemoPlugin) Load(host plugin.Host) error {
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now()}
	host.Logger().Info("card-rules-demo plugin loaded — 2 card rules registered via manifest")
	return nil
}

func (p *CardRulesDemoPlugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	return nil
}

func (p *CardRulesDemoPlugin) Status() plugin.PluginStatus { return p.status }
