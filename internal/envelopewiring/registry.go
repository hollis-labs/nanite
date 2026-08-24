package envelopewiring

import (
	"github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/envelope"
)

// RegistryTarget accepts the shared go-envelopes Registry at composition time.
type RegistryTarget interface {
	SetEnvelopeRegistry(*envelopes.Registry)
}

// InstallSharedRegistry wires one go-envelopes Registry instance into every
// package that consumes the startup envelope catalog.
func InstallSharedRegistry(reg *envelopes.Registry, pluginHost RegistryTarget) {
	chat.SetEnvelopeRegistry(reg)
	envelope.SetEnvelopeRegistry(reg)
	if pluginHost != nil {
		pluginHost.SetEnvelopeRegistry(reg)
	}
}
