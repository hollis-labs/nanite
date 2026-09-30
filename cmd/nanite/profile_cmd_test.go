package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
)

func TestProfileShow(t *testing.T) {
	reg, err := harnessprofile.NewRegistry("", "")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := profileShow(reg, []string{"conservative", "--model", "claude-opus-5"}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"profile  conservative", "sha256:", "model    claude-opus-5", "hard_ceiling", "profile:conservative", "computed"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	// Flags before the name work too, and --json is the recorded form.
	out.Reset()
	if err := profileShow(reg, []string{"--json", "dev"}, &out); err != nil {
		t.Fatal(err)
	}
	var eff harnessprofile.Effective
	if err := json.Unmarshal(out.Bytes(), &eff); err != nil || eff.Profile != "dev" || eff.Values["hard_ceiling"].(float64) != 400 {
		t.Errorf("json form: %v %+v", err, eff)
	}
	// No name shows the registry default; an unknown name is an error.
	out.Reset()
	if err := profileShow(reg, nil, &out); err != nil || !strings.Contains(out.String(), "profile  default") {
		t.Errorf("default: %v\n%s", err, out.String())
	}
	if err := profileShow(reg, []string{"nope"}, &out); err == nil || !strings.Contains(err.Error(), "unknown harness profile") {
		t.Errorf("unknown: %v", err)
	}
}

func TestBuildHarnessRegistryHonorsEnvDefault(t *testing.T) {
	t.Setenv("NANITE_HARNESS_PROFILE", "dev")
	appCfg := config.DefaultAppConfig()
	reg, err := buildHarnessRegistry(appCfg, t.TempDir())
	if err != nil || reg.DefaultName() != "dev" {
		t.Fatalf("default = %v, %v", reg, err)
	}
	t.Setenv("NANITE_HARNESS_PROFILE", "missing")
	if _, err := buildHarnessRegistry(appCfg, t.TempDir()); err == nil {
		t.Error("an unknown default profile must fail at startup")
	}
}
