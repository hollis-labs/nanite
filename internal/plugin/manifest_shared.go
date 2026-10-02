package plugin

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
	modsemver "golang.org/x/mod/semver"
)

// DecodeManifest reads the shared generated plugin.yaml declaration. Its JSON
// serialization is valid YAML; other manifest dialects are refused.
func DecodeManifest(reader io.Reader) (*PluginManifest, error) {
	common, err := manifest.Decode(reader)
	if err != nil {
		return nil, fmt.Errorf("shared manifest: %w", err)
	}
	if idErr := ValidatePluginID(common.ID); idErr != nil {
		return nil, idErr
	}
	hostRange, ok := common.Hosts["nanite"]
	if !ok {
		return nil, fmt.Errorf("shared manifest does not target nanite")
	}
	if rangeErr := CheckHostRange(hostRange); rangeErr != nil {
		return nil, rangeErr
	}
	block, err := pluginapi.DecodeBlock(common.Nanite)
	if err != nil {
		return nil, err
	}
	result := &PluginManifest{
		SchemaVersion: common.SchemaVersion, ID: common.ID, Name: common.Name,
		Version: common.Version, Description: common.Description, License: common.License,
		Homepage: common.Homepage, Repository: common.Repository, Protocol: common.Protocol,
		Runtime: common.Runtime, Entrypoint: common.Entrypoint.Command,
		EntrypointArgs: append([]string(nil), common.Entrypoint.Args...), Shared: &common,
		Config: make(map[string]ConfigEntry), LoadType: LoadType(block.LoadType),
		UI: ManifestUI{Entry: block.UI.Bundle, Stylesheet: block.UI.Stylesheet, ReactVersion: block.UI.ReactVersion, ShadcnVersion: block.UI.ShadcnVersion},
	}
	for name, field := range common.Config.Fields {
		result.Config[name] = ConfigEntry{Type: field.Type, Required: field.Required, EnvVar: field.Env, Default: field.Default, Description: field.Description}
	}
	for name, secret := range common.Config.Secrets {
		result.Config[name] = ConfigEntry{Type: "string", Required: secret.Required, EnvVar: secret.Env, Description: secret.Description, Secret: true}
	}
	for _, slot := range block.Registers.Slots {
		var props map[string]any
		if len(slot.Props) > 0 {
			if err := json.Unmarshal(slot.Props, &props); err != nil {
				return nil, err
			}
		}
		result.Registers.Slots = append(result.Registers.Slots, SlotRegistration{Slot: slot.Slot, ID: slot.ID, Title: slot.Title, Icon: slot.Icon, Component: slot.Component, Priority: slot.Priority, Props: props})
	}
	for _, panel := range block.Registers.Panels {
		result.Registers.Panels = append(result.Registers.Panels, PanelRegistration{ID: panel.ID, Title: panel.Title, Component: panel.Component, Icon: panel.Icon, Description: panel.Description, DefaultVisible: panel.DefaultVisible, Order: panel.Order})
	}
	for _, envelope := range block.Registers.Envelopes {
		result.Registers.Envelopes = append(result.Registers.Envelopes, EnvelopeRegistration{Type: envelope.Type, Component: envelope.Component, Version: envelope.Version, Schema: envelope.Schema})
	}
	for _, command := range block.Registers.Commands {
		result.Registers.Commands = append(result.Registers.Commands, CommandRegistration{Name: command.Name, Description: command.Description, Aliases: append([]string(nil), command.Aliases...), Hidden: command.Hidden})
	}
	for _, event := range block.Registers.Events {
		result.Registers.Events = append(result.Registers.Events, EventRegistration{Types: append([]string(nil), event.Types...), Priority: event.Priority})
	}
	for _, resource := range block.Registers.CRUD {
		result.Registers.Crud = append(result.Registers.Crud, CRUDRegistration{Resource: resource.Name, Methods: append([]string(nil), resource.Methods...)})
	}
	for _, route := range block.Registers.HTTPRoutes {
		result.Registers.HttpRoutes = append(result.Registers.HttpRoutes, HTTPRouteRegistration{Method: route.Method, Pattern: "/api/plugins/" + common.ID + "/" + route.Path})
	}
	return result, nil
}

// CheckHostRange enforces the public contract version independently of the app.
func CheckHostRange(required manifest.HostRange) error {
	current := "v" + pluginapi.Version
	minimum, maximum := required.Min, required.Max
	if minimum != "" && !manifest.ValidVersion(minimum) || maximum != "" && !manifest.ValidVersion(maximum) {
		return fmt.Errorf("nanite host range requires semantic versions")
	}
	if minimum != "" && maximum != "" && modsemver.Compare("v"+minimum, "v"+maximum) > 0 {
		return fmt.Errorf("nanite host range minimum exceeds maximum")
	}
	if minimum != "" && modsemver.Compare(current, "v"+minimum) < 0 || maximum != "" && modsemver.Compare(current, "v"+maximum) > 0 {
		return fmt.Errorf("plugin host range does not include nanite contract %s", pluginapi.Version)
	}
	return nil
}
