//go:build !darwin

package main

import (
	"context"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
)

// keychainHas looks the entry up through the OS credential store (Secret Service, Credential
// Manager); only macOS has an attributes-only check.
func keychainHas(ctx context.Context, service, account string) (bool, bool, error) {
	_, err := libsecrets.NewKeyringStore().Get(ctx, service, account)
	if err == nil {
		return true, true, nil
	}
	if libsecrets.KindOf(err) == libsecrets.KindNotFound {
		return false, true, nil
	}
	return false, true, err
}
