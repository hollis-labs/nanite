// Package secrets provides OS keychain-backed credential storage.
// Uses macOS Keychain, Windows Credential Manager, or Linux Secret Service.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	shared "github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/redaction"
)

var serviceName = brand.ID

// Set stores a secret in the OS keychain under the given key.
func Set(key, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	redaction.Sink{}.Add(value)
	if err := shared.NewKeyringStore().Set(ctx, serviceName, key, shared.FromString(value)); err != nil {
		return fmt.Errorf("keyring set %q: %w", key, err)
	}
	return nil
}

// Get retrieves a secret from the OS keychain. Returns empty string if not found.
func Get(key string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	val, err := shared.NewKeyringStore().Get(ctx, serviceName, key)
	if err == nil {
		redaction.Sink{}.Add(val.Reveal())
		return val.Reveal()
	}
	if errors.Is(err, shared.ErrNotFound) {
		slog.Debug("secrets: key not found", "key", key)
		return ""
	}
	slog.Warn("secrets: get failed", "key", key, "err", err)
	return ""
}

// Delete removes a secret from the OS keychain. No error if not found.
func Delete(key string) {
	if err := DeleteChecked(key); err != nil {
		slog.Warn("secrets: delete failed", "key", key, "err", err)
	}
}

// DeleteChecked reports backend failures to callers that must not claim a
// credential was removed when the keychain is unavailable. Missing is success.
func DeleteChecked(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shared.NewKeyringStore().Delete(ctx, serviceName, key); err != nil && !errors.Is(err, shared.ErrNotFound) {
		return fmt.Errorf("keyring delete %q: %w", key, err)
	}
	return nil
}

// Has returns true if a secret exists in the OS keychain for the given key.
func Has(key string) bool {
	return Get(key) != ""
}

// ProviderKeyName returns the keychain key for a provider's API key.
func ProviderKeyName(providerID string) string {
	return "provider-api-key:" + providerID
}
