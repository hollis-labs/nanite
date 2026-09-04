package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/mcp"
)

func TestRegisterTesseractServerUsesReleasedStdioCommandAndEnvironmentToken(t *testing.T) {
	t.Setenv(brand.Env("TESSERACT_TOKEN"), "environment-token")
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	command := filepath.Join(dir, "fake-tesseract")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + strconv.Quote(argsFile) + "\n" +
		"while IFS= read -r request; do\n" +
		"  printf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[]}}'\n" +
		"done\n"
	if err := os.WriteFile(command, []byte(script), 0o600); err != nil {
		t.Fatalf("write fake tesseract: %v", err)
	}
	// #nosec G302 -- this test fixture must be executable by its owner.
	if err := os.Chmod(command, 0o700); err != nil {
		t.Fatalf("make fake tesseract executable: %v", err)
	}

	manager := mcp.NewManager()
	t.Cleanup(manager.Close)
	registerTesseractServer(manager, &config.RuntimeConfig{Tesseract: config.TesseractConfig{
		Command: command, Token: "yaml-token",
	}})
	if !manager.HasServer("tesseract") {
		t.Fatal("default tesseract server was not registered")
	}
	if err := manager.DiscoverTools(t.Context()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	// #nosec G304 -- argsFile is an exact path inside t.TempDir.
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if got := strings.Fields(string(args)); strings.Join(got, "|") != "mcp|--token|environment-token" {
		t.Fatalf("tesseract args = %q", got)
	}
}

func TestRegisterTesseractServerIsOptIn(t *testing.T) {
	manager := mcp.NewManager()
	t.Cleanup(manager.Close)
	registerTesseractServer(manager, &config.RuntimeConfig{})
	if manager.HasServer("tesseract") {
		t.Fatal("tesseract server registered without command")
	}
}
