package elicitation

import (
	"fmt"

	"github.com/google/uuid"
)

// newID generates a fresh unique elicitation request ID.
func newID() string {
	return uuid.New().String()
}

// errElicitEmit wraps an emit error for consistent error messages.
func errElicitEmit(err error) error {
	return fmt.Errorf("elicitation: emit: %w", err)
}
