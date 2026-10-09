package subprocess

import (
	"context"
	"fmt"
	"strings"
	"sync"

	pluginhost "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
)

// Process-wide allocation survives controller recreation. A new host process
// receives a fresh random epoch. These identities identify lifetimes; they do
// not authorize capabilities or authenticate callers.
var processOwners struct {
	once        sync.Once
	epoch       string
	err         error
	generations pluginhost.MemoryGenerationStore
}

func HostInstance() (string, error) {
	processOwners.once.Do(func() {
		processOwners.epoch, processOwners.err = pluginhost.NewHostInstance()
	})
	return processOwners.epoch, processOwners.err
}

func NewOwnerIncarnation(ctx context.Context, id string) (capability.RuntimeIdentity, error) {
	if strings.TrimSpace(id) == "" {
		return capability.RuntimeIdentity{}, fmt.Errorf("plugin owner ID is required before launch")
	}
	epoch, err := HostInstance()
	if err != nil {
		return capability.RuntimeIdentity{}, err
	}
	generation, err := processOwners.generations.Next(ctx, epoch, id)
	if err != nil {
		return capability.RuntimeIdentity{}, err
	}
	return capability.RuntimeIdentity{HostInstance: epoch, OwnerID: id, OwnerGeneration: generation}, nil
}
