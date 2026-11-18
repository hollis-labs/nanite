// Package descriptor loads authored project/1 descriptors using Substrate's
// shared structural contract. Class, auth, deployment and runtime conformance
// remain the responsibility of the generated checks and runtime guards.
package descriptor

import (
	"fmt"
	"os"

	"github.com/hollis-labs/substrate/kinds"
)

// Load reads and structurally validates a descriptor without rewriting it.
func Load(path string) (*kinds.Project, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse uses the same project/1 contract as other Substrate consumers. Successful
// parsing is not class-bar certification or permission to deploy the project.
func Parse(b []byte) (*kinds.Project, error) {
	p, err := kinds.ParseProjectYAML(b)
	if err != nil {
		return nil, fmt.Errorf("project.yaml: %w", err)
	}
	return p, nil
}
