package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/panics"
)

func TestGuardedMainExitsWithRefOnPanic(t *testing.T) {
	var stderr bytes.Buffer
	code := guardedMain(context.Background(), func(context.Context) error { panic("sk-ant-secretvalue0123456789abcdef") }, &stderr)
	if code != panics.ExitSoftware || !strings.Contains(stderr.String(), "internal error (ref pnc_") || strings.Contains(stderr.String(), "secretvalue0123456789") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if code := guardedMain(context.Background(), func(context.Context) error { return errors.New("plain") }, &stderr); code != 1 {
		t.Fatalf("plain error exit %d", code)
	}
	if code := guardedMain(context.Background(), func(context.Context) error { return nil }, &stderr); code != 0 {
		t.Fatalf("success exit %d", code)
	}
}
