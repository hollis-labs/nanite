package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/modelsdevtest"
	naniteotel "github.com/hollis-labs/nanite/internal/otel"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
)

// containerGoroutines counts the background loops a service container runs
// that write the store: the two reapers and the model catalog refresher.
func containerGoroutines(t *testing.T) int {
	t.Helper()
	var stacks bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
		t.Fatalf("write goroutine profile: %v", err)
	}
	profile := stacks.String()
	return strings.Count(profile, "internal/subagent.(*Reaper).loop") +
		strings.Count(profile, "internal/recovery/orphansweep.(*RuntimeReaper).loop") +
		strings.Count(profile, "go-modelsdev/modelsdev.(*Client).Run")
}

// CW-20260930-0105: an error return after the container is built — here
// ListenAndServe failing on a port already in use, after the full boot —
// shuts the container down before the deferred store close, as the signal
// path does. Before, its reapers and refresher outlived the store.
func TestCmdServeErrorReturnShutsDownContainer(t *testing.T) {
	root := t.TempDir()
	for env, dir := range map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(root, "config"),
		"XDG_DATA_HOME":   filepath.Join(root, "data"),
		"XDG_STATE_HOME":  filepath.Join(root, "state"),
		"XDG_CACHE_HOME":  filepath.Join(root, "cache"),
	} {
		t.Setenv(env, dir)
	}
	t.Setenv(modelsDevURLEnv, modelsdevtest.NewServer(t).URL)

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := strconv.Itoa(busy.Addr().(*net.TCPAddr).Port)

	before := containerGoroutines(t)
	err = cmdServeWithInitializers(
		[]string{"--db", filepath.Join(root, "nanite.db"), "--dev", "--port", port},
		func(slogx.Config) (*slog.Logger, io.Closer, error) {
			return slog.Default(), &countingCloser{}, nil
		},
		func(context.Context, naniteotel.Config) (func(context.Context) error, error) {
			return func(context.Context) error { return nil }, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "server error") {
		t.Fatalf("cmdServe error = %v, want the ListenAndServe failure", err)
	}

	// Reaper Stop returns as the loop's deferred closeDone runs, just before
	// its frame unwinds, so allow a moment.
	deadline := time.Now().Add(2 * time.Second)
	for {
		after := containerGoroutines(t)
		if after <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("container goroutines outlived cmdServe's error return: %d -> %d", before, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestModelCatalogOptionsFromEnv(t *testing.T) {
	t.Setenv(modelsDevURLEnv, "")
	if opts := modelCatalogOptionsFromEnv(); opts != nil {
		t.Fatalf("unset %s: %d options, want none (the live catalog)", modelsDevURLEnv, len(opts))
	}
	t.Setenv(modelsDevURLEnv, "  http://mirror.local/api.json ")
	if opts := modelCatalogOptionsFromEnv(); len(opts) != 1 {
		t.Fatalf("set %s: %d options, want WithURL", modelsDevURLEnv, len(opts))
	}
}

func TestTeamStartupUnavailableDoesNotHideRecoveryFailures(t *testing.T) {
	if err := teamStartupRecoveryError(service.TeamRunReconcileReport{Failures: []error{store.ErrVerifiedActorRequired}}); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("private persistence failure")
	if err := teamStartupRecoveryError(service.TeamRunReconcileReport{Failures: []error{errors.Join(store.ErrVerifiedActorRequired, failed)}}); !errors.Is(err, failed) {
		t.Fatalf("recovery error=%v", err)
	}
}
