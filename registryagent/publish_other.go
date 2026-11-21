//go:build !linux && !darwin

package registryagent

import (
	"fmt"
	"os"
)

func publish(_ *os.File, _, _ string, _ bool) error {
	return fmt.Errorf("registryagent: atomic directory publication is not supported on this platform")
}
