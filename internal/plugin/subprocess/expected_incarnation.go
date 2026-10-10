package subprocess

import (
	"context"
	"errors"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
)

var ErrStaleBinding = errors.New("stale plugin tool binding: discover tools again")

type expectedIncarnationKey struct{}

// WithExpectedIncarnation pins a dispatch to the owner in a host-issued catalog
// binding. It is a lifecycle fence, never authentication or a capability grant.
func WithExpectedIncarnation(ctx context.Context, expected capability.RuntimeIdentity) context.Context {
	return context.WithValue(ctx, expectedIncarnationKey{}, expected)
}

func expectedIncarnation(ctx context.Context) (capability.RuntimeIdentity, bool) {
	expected, present := ctx.Value(expectedIncarnationKey{}).(capability.RuntimeIdentity)
	return expected, present
}

type dispatchValidationKey struct{}

// WithDispatchValidation carries an internal execution-owner revalidation
// callback. It grants no authority and does not replace the owner's transaction
// or receipt guard. The callback runs outside manager and lease locks.
func WithDispatchValidation(ctx context.Context, validate func(context.Context) error) context.Context {
	previous, _ := ctx.Value(dispatchValidationKey{}).(func(context.Context) error)
	return context.WithValue(ctx, dispatchValidationKey{}, func(call context.Context) error {
		if previous != nil {
			if err := previous(call); err != nil {
				return err
			}
		}
		if validate == nil {
			return nil
		}
		return validate(call)
	})
}

func validateDispatch(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if validate, ok := ctx.Value(dispatchValidationKey{}).(func(context.Context) error); ok {
		return validate(ctx)
	}
	return nil
}
