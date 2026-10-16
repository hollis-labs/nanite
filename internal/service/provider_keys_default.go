package service

import (
	"github.com/hollis-labs/nanite/internal/providerkeys"
)

// ProviderKeyEnvName is the conventional environment variable for a provider
// row, "" for a row Nanite has no adapter for.
func ProviderKeyEnvName(providerID string) string {
	if spec, ok := APIProviderSpecByID(providerID); ok {
		return spec.EnvKey
	}
	return ""
}

// DefaultProviderKeys builds the production key manager: the OS keychain, the
// key_ref in st's provider rows, and the environment only when the operator
// enabled it (providerkeys.EnvEnableVar). st may be nil.
func DefaultProviderKeys(st ProviderStore) *providerkeys.Manager {
	opts := providerkeys.Options{
		EnvNames:   ProviderKeyEnvName,
		EnvEnabled: providerkeys.FromEnvironment(),
	}
	if st != nil {
		opts.Settings = st
	}
	return providerkeys.New(opts)
}
