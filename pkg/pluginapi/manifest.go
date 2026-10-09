// Package pluginapi is Nanite's public subprocess-plugin contract. It has no
// dependency on Nanite internals. Plugins emit a Block in the shared manifest's
// nanite object; hosts validate it before applying registrations.
package pluginapi

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

// Version identifies the public host contract, independently of the Nanite
// application version. A plugin declares this range in hosts.nanite.
const Version = "0.2.0"

// Drawer slots are host-owned browser contribution locations.
const (
	SlotPrimaryDrawer = "drawer.primary.tabs"
	SlotWorkingDrawer = "drawer.working.tabs"
)

// Block is the shared manifest's nanite extension. Common config, secrets,
// capabilities, tools and execution metadata belong to the shared manifest.
// Registers is declarative: a plugin cannot add undeclared registrations at
// runtime. The host must resolve conflicts and retain ownership for unloading.
type Block struct {
	UI        UI            `json:"ui,omitzero"`
	Registers Registrations `json:"registers,omitzero"`
	LoadType  LoadType      `json:"load_type,omitempty"`
}

type LoadType string

const (
	LoadAuto  LoadType = "auto"
	LoadOptIn LoadType = "opt-in"
)

type UI struct {
	Bundle        string `json:"bundle,omitempty"`
	Stylesheet    string `json:"stylesheet,omitempty"`
	ReactVersion  string `json:"react_version,omitempty"`
	ShadcnVersion string `json:"shadcn_version,omitempty"`
}

type Registrations struct {
	Slots             []Slot             `json:"slots,omitempty"`
	Panels            []Panel            `json:"panels,omitempty"`
	Envelopes         []Envelope         `json:"envelopes,omitempty"`
	Commands          []Command          `json:"commands,omitempty"`
	Events            []Event            `json:"events,omitempty"`
	CRUD              []Resource         `json:"crud,omitempty"`
	HTTPRoutes        []Route            `json:"http_routes,omitempty"`
	ContextSources    []ContextSource    `json:"context_sources,omitempty"`
	AlwaysShipSources []AlwaysShipSource `json:"always_ship_sources,omitempty"`
	ReflexSeeds       []ReflexSeed       `json:"reflex_seeds,omitempty"`
}

// Component names refer directly to named exports of UI.Bundle.
type Slot struct {
	ID        string          `json:"id"`
	Slot      string          `json:"slot"`
	Component string          `json:"component"`
	Title     string          `json:"title,omitempty"`
	Icon      string          `json:"icon,omitempty"`
	Priority  int             `json:"priority,omitempty"`
	Props     json.RawMessage `json:"props,omitempty"`
}

type Panel struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Component      string `json:"component"`
	Icon           string `json:"icon,omitempty"`
	Description    string `json:"description,omitempty"`
	DefaultVisible bool   `json:"default_visible,omitempty"`
	Order          int    `json:"order,omitempty"`
}

type Envelope struct {
	Type      string `json:"type"`
	Component string `json:"component"`
	Version   int    `json:"version"`
	Schema    string `json:"schema"`
}

type Command struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases,omitempty"`
	Hidden      bool     `json:"hidden,omitempty"`
}

type Event struct {
	Types    []string `json:"types"`
	Priority int      `json:"priority,omitempty"`
}

type Resource struct {
	Name    string   `json:"resource"`
	Methods []string `json:"methods"`
}

// Route is confined by the host under /api/plugins/<plugin-id>/. Its path is
// relative to that namespace. It cannot claim a core host API route.
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

var slug = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var export = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Validate checks the extension without reading a bundle or granting access.
// Hosts additionally validate slot availability, registry conflicts, UI runtime
// compatibility, envelope schemas and real filesystem confinement.
func (b Block) Validate() error {
	if b.LoadType != "" && b.LoadType != LoadAuto && b.LoadType != LoadOptIn {
		return fmt.Errorf("nanite: invalid load_type %q", b.LoadType)
	}
	for _, asset := range []string{b.UI.Bundle, b.UI.Stylesheet} {
		if asset != "" && !bundlePath(asset) {
			return fmt.Errorf("nanite: UI asset %q must stay inside the bundle", asset)
		}
	}
	if len(b.Registers.Slots)+len(b.Registers.Panels)+len(b.Registers.Envelopes) > 0 && b.UI.Bundle == "" {
		return fmt.Errorf("nanite: UI registrations require ui.bundle")
	}
	seen := map[string]bool{}
	check := func(kind, id string) error {
		key := kind + ":" + id
		if !slug.MatchString(id) {
			return fmt.Errorf("nanite: invalid %s id %q", kind, id)
		}
		if seen[key] {
			return fmt.Errorf("nanite: duplicate %s id %q", kind, id)
		}
		seen[key] = true
		return nil
	}
	for _, s := range b.Registers.Slots {
		if err := check("slot", s.ID); err != nil {
			return err
		}
		if strings.TrimSpace(s.Slot) == "" || !export.MatchString(s.Component) {
			return fmt.Errorf("nanite: slot %q requires a slot name and component export", s.ID)
		}
		if len(s.Props) > 0 {
			var props map[string]json.RawMessage
			if json.Unmarshal(s.Props, &props) != nil || props == nil {
				return fmt.Errorf("nanite: slot %q props must be an object", s.ID)
			}
		}
	}
	for _, p := range b.Registers.Panels {
		if err := check("panel", p.ID); err != nil {
			return err
		}
		if strings.TrimSpace(p.Title) == "" || !export.MatchString(p.Component) {
			return fmt.Errorf("nanite: panel %q requires title and component export", p.ID)
		}
	}
	for _, e := range b.Registers.Envelopes {
		if err := check("envelope", e.Type); err != nil {
			return err
		}
		if e.Version < 1 || !export.MatchString(e.Component) || !bundlePath(e.Schema) {
			return fmt.Errorf("nanite: envelope %q requires version, component export and bundle schema", e.Type)
		}
	}
	for _, c := range b.Registers.Commands {
		if err := check("command", c.Name); err != nil {
			return err
		}
		if strings.TrimSpace(c.Description) == "" {
			return fmt.Errorf("nanite: command %q requires description", c.Name)
		}
		for _, alias := range c.Aliases {
			if err := check("command", alias); err != nil {
				return err
			}
		}
	}
	for _, e := range b.Registers.Events {
		if len(e.Types) == 0 {
			return fmt.Errorf("nanite: event subscription requires types")
		}
		for _, event := range e.Types {
			if strings.TrimSpace(event) == "" || seen["event:"+event] {
				return fmt.Errorf("nanite: empty or duplicate event type %q", event)
			}
			seen["event:"+event] = true
		}
	}
	for _, r := range b.Registers.CRUD {
		if err := check("resource", r.Name); err != nil {
			return err
		}
		if len(r.Methods) == 0 {
			return fmt.Errorf("nanite: resource %q requires methods", r.Name)
		}
		for _, method := range r.Methods {
			switch method {
			case "create", "read", "update", "delete", "list":
			default:
				return fmt.Errorf("nanite: unknown CRUD method %q", method)
			}
			key := "resource-method:" + r.Name + ":" + method
			if seen[key] {
				return fmt.Errorf("nanite: duplicate CRUD method %q", method)
			}
			seen[key] = true
		}
	}
	if len(b.Registers.ContextSources) > MaxContextSources {
		return fmt.Errorf("nanite: too many context sources")
	}
	for _, source := range b.Registers.ContextSources {
		if err := check("context-source", source.ID); err != nil {
			return err
		}
		if len(source.ID) > 64 {
			return fmt.Errorf("nanite: context source id exceeds limit")
		}
	}
	if len(b.Registers.AlwaysShipSources) > MaxAlwaysShipSources {
		return fmt.Errorf("nanite: too many always-ship sources")
	}
	alwaysShipTitles := make(map[string]bool)
	for _, source := range b.Registers.AlwaysShipSources {
		// One source cannot acquire both weaker and persistent placement.
		if err := check("context-source", source.ID); err != nil {
			return err
		}
		if err := source.Validate(); err != nil {
			return err
		}
		title := normalizedAlwaysShipTitle(source.Title)
		if alwaysShipTitles[title] {
			return fmt.Errorf("nanite: duplicate always-ship title")
		}
		alwaysShipTitles[title] = true
	}
	if len(b.Registers.ReflexSeeds) > MaxReflexSeeds {
		return fmt.Errorf("nanite: too many reflex seeds")
	}
	for _, seed := range b.Registers.ReflexSeeds {
		if err := check("reflex-seed", seed.ID); err != nil {
			return err
		}
		if err := seed.Validate(); err != nil {
			return err
		}
	}
	for _, r := range b.Registers.HTTPRoutes {
		switch r.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			return fmt.Errorf("nanite: invalid route method %q", r.Method)
		}
		if !bundlePath(r.Path) || strings.ContainsAny(r.Path, "?#%{}") {
			return fmt.Errorf("nanite: route path %q must be relative to the plugin namespace", r.Path)
		}
		normalized := path.Clean(r.Path)
		if strings.HasSuffix(r.Path, "/") {
			normalized += "/"
		}
		if normalized != r.Path {
			return fmt.Errorf("nanite: route path %q must use canonical segments", r.Path)
		}
		key := "route:" + r.Method + ":" + normalized
		if seen[key] {
			return fmt.Errorf("nanite: duplicate route %q", key)
		}
		seen[key] = true
	}
	return nil
}

// EncodeBlock builds the opaque extension value for manifest.Manifest.Nanite.
func EncodeBlock(b Block) (json.RawMessage, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(b)
}

// DecodeBlock decodes a block after the shared manifest decoder has checked
// duplicate keys and bounded the document. Unknown extension fields are errors.
func DecodeBlock(raw json.RawMessage) (Block, error) {
	var b Block
	if err := manifest.DecodeExtension(raw, &b); err != nil {
		return Block{}, err
	}
	if err := b.Validate(); err != nil {
		return Block{}, err
	}
	return b, nil
}

func bundlePath(p string) bool {
	if p == "" || path.Clean(p) == "." || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:\x00\n\r\t ") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}
