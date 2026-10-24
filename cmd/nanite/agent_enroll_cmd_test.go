package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tetherregistry"
	"github.com/hollis-labs/nanite/internal/tetherregistry/tetherregistrytest"
)

func enrollCLIFixture(t *testing.T) (*service.EnrollmentService, *tetherregistrytest.Fake, store.AgentHostSettings) {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeStoreBestEffort(ctx, st) })
	def := []byte("---\nschema_version: \"2\"\ndefinition_id: def:cli-enroll\nrevision: \"1\"\nname: cli-enroll\ndescription: Fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture.\n")
	pin, err := st.InstallAgentDefinition(ctx, def, nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: "cli-enroll", Title: "CLI enroll", DefinitionRef: pin,
		Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture", Model: "m"}, Enabled: true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	fake := tetherregistrytest.New(t)
	svc := &service.EnrollmentService{Store: st, Registrar: fake.Registrar(tetherregistrytest.TokenFile(t))}
	return svc, fake, host
}

func TestAgentEnrollCLIEnrollsAndIsIdempotent(t *testing.T) {
	svc, fake, host := enrollCLIFixture(t)
	var first, second bytes.Buffer
	if err := runAgentEnroll(context.Background(), svc, []string{host.ID, "--reason", "cli test"}, &first); err != nil {
		t.Fatal(err)
	}
	// Flags may come first.
	if err := runAgentEnroll(context.Background(), svc, []string{"--reason", "again", host.ID}, &second); err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	_ = json.Unmarshal(first.Bytes(), &a)
	_ = json.Unmarshal(second.Bytes(), &b)
	if a["urn"] == "" || a["urn"] != b["urn"] || a["already_enrolled"] != false || b["already_enrolled"] != true {
		t.Fatalf("first=%v second=%v", a, b)
	}
	if len(fake.Entries()) != 1 {
		t.Fatalf("tether entries = %d", len(fake.Entries()))
	}
	if strings.Contains(first.String()+second.String(), tetherregistrytest.Token) {
		t.Fatal("output leaked the token")
	}
}

func TestAgentEnrollCLIRefusals(t *testing.T) {
	svc, _, host := enrollCLIFixture(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := runAgentEnroll(ctx, svc, nil, &out); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("no id: %v", err)
	}
	if err := runAgentEnroll(ctx, svc, []string{host.ID}, &out); !errors.Is(err, service.ErrEnrollmentReasonRequired) {
		t.Fatalf("no reason: %v", err)
	}
	if err := runAgentEnroll(ctx, svc, []string{host.ID, "--reason", "r", "--token", "x"}, &out); err == nil {
		t.Fatal("a token flag was accepted; the credential must come only from its 0600 file")
	}
	svc.Registrar = &tetherregistry.Client{Config: func() tetherregistry.Config { return tetherregistry.Config{} }}
	if err := runAgentEnroll(ctx, svc, []string{host.ID, "--reason", "r"}, &out); !errors.Is(err, tetherregistry.ErrNotConfigured) {
		t.Fatalf("not configured: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("refusals printed %q", out.String())
	}
}
