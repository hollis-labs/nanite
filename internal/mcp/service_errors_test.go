package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	svcerr "github.com/hollis-labs/go-svcerr"
)

func TestServiceErrorResultKeepsCausePrivate(t *testing.T) {
	cause := errors.New("private key and database query")
	for _, err := range []error{cause, fmt.Errorf("operation: %w", svcerr.Wrap(cause, svcerr.CodeNotFound, "todo not found"))} {
		result := ServiceErrorResult(err)
		if !result.IsError || len(result.Content) != 1 {
			t.Fatalf("failure result: %+v", result)
		}
		if strings.Contains(result.Content[0].Text, cause.Error()) {
			t.Fatalf("leaked cause: %s", result.Content[0].Text)
		}
	}
}
