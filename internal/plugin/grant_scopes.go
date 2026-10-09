package plugin

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

// NaniteGrantSchemaVersion versions this host's opaque Grant.Scope descriptors.
// These DTOs are not the shared capability.Scope schema. Their exact fields and
// endpoint checks remain authoritative, including dynamic current-workspace
// AllSessions and the aggregate always-ship byte ceiling.
const NaniteGrantSchemaVersion uint32 = 1
const naniteGrantPrefix = "host.nanite."

type environmentGrantScope struct {
	Environment []string `json:"environment"`
}

func reviewedGrantScope(name string, raw json.RawMessage) (json.RawMessage, error) {
	var err error
	switch name {
	case pluginapi.CapabilityReadOnlyQuery:
		_, err = pluginapi.DecodeQueryScope(raw)
	case pluginapi.CapabilityContextSource:
		_, err = pluginapi.DecodeContextScope(raw)
	case pluginapi.CapabilityContextAlwaysShip:
		_, err = pluginapi.DecodeAlwaysShipScope(raw)
	case pluginapi.CapabilityReflexSeed:
		_, err = pluginapi.DecodeReflexScope(raw)
	case pluginapi.CapabilityDurableWake:
		_, err = pluginapi.DecodeDurableWakeScope(raw)
	case "ssh_agent", "docker_socket":
		// These declarations have no caller-selectable scope. The accepted
		// host descriptor enumerates the exact environment keys permitted by
		// that capability; unavailable values are never synthesized.
		var empty struct{}
		if err = manifest.DecodeExtension(raw, &empty); err == nil {
			return json.Marshal(environmentGrantScope{Environment: slices.Clone(capabilityEnvironment[name])})
		}
	default:
		err = fmt.Errorf("unsupported Nanite capability descriptor")
	}
	if err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), raw...), nil
}
