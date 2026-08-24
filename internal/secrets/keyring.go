// Package secrets provides OS keychain-backed credential storage.
// Uses macOS Keychain, Windows Credential Manager, or Linux Secret Service.
package secrets

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/zalando/go-keyring"
)

var serviceName = brand.ID

// Set stores a secret in the OS keychain under the given key.
func Set(key, value string) error {
	if err := keyring.Set(serviceName, key, value); err != nil {
		return fmt.Errorf("keyring set %q: %w", key, err)
	}
	return nil
}

// Get retrieves a secret from the OS keychain. Returns empty string if not found.
func Get(key string) string {
	val, err := keyring.Get(serviceName, key)
	if err == nil {
		return val
	}
	if errors.Is(err, keyring.ErrNotFound) {
		slog.Debug("secrets: key not found", "key", key)
		return ""
	}
	slog.Warn("secrets: get failed", "key", key, "err", err)
	return ""
}

// Delete removes a secret from the OS keychain. No error if not found.
func Delete(key string) {
	if err := keyring.Delete(serviceName, key); err != nil {
		slog.Warn("secrets: delete failed", "key", key, "err", err)
	}
}

// Has returns true if a secret exists in the OS keychain for the given key.
func Has(key string) bool {
	return Get(key) != ""
}

// ProviderKeyName returns the keychain key for a provider's API key.
func ProviderKeyName(providerID string) string {
	return "provider-api-key:" + providerID
}
