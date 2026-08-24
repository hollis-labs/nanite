package envelopewiring

import (
	"net/http"
	"testing"

	"github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/envelope"
	"github.com/hollis-labs/nanite/internal/plugin"
)

func TestInstallSharedRegistryWiresAllConsumersToSameInstance(t *testing.T) {
	reg := envelopes.NewRegistry()
	host := plugin.NewHost(http.NewServeMux(), plugin.NewLogger("registry-wiring-test"))

	previousChatRegistry := chat.EnvelopeRegistry()
	previousEnvelopeRegistry := envelope.EnvelopeRegistry()
	t.Cleanup(func() {
		chat.SetEnvelopeRegistry(previousChatRegistry)
		envelope.SetEnvelopeRegistry(previousEnvelopeRegistry)
	})

	InstallSharedRegistry(reg, host)

	if got := chat.EnvelopeRegistry(); got != reg {
		t.Fatalf("chat registry = %p, want %p", got, reg)
	}
	if got := envelope.EnvelopeRegistry(); got != reg {
		t.Fatalf("envelope validator registry = %p, want %p", got, reg)
	}
	if got := host.EnvelopeRegistry(); got != reg {
		t.Fatalf("plugin host registry = %p, want %p", got, reg)
	}
}
