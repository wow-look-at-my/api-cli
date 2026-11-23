// keyring.go holds the keyring template function.

package main

import (
	"fmt"

	"github.com/zalando/go-keyring"
)

// keyringSecret reads one secret from the OS keyring: the macOS Keychain, the
// Secret Service on Linux, or the Windows Credential Manager. A missing item
// or an unreachable keyring fails the render.
func keyringSecret(service, account string) (string, error) {
	s, err := keyring.Get(service, account)
	if err != nil {
		return "", fmt.Errorf("keyring %q %q: %w", service, account, err)
	}
	return s, nil
}
