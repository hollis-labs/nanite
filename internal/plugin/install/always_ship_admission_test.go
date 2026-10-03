package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func admissionBundle(t *testing.T, owner string, titles ...string) string {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(validManifest()), &object); err != nil {
		t.Fatal(err)
	}
	object["id"], _ = json.Marshal(owner)
	object["tools"] = json.RawMessage(`[{"name":"list","description":"Inventory","effect":"read","input_schema":{"type":"object"}}]`)
	var sources []pluginapi.AlwaysShipSource
	var ids []string
	for i, title := range titles {
		id := fmt.Sprintf("s%d", i)
		ids = append(ids, id)
		sources = append(sources, pluginapi.AlwaysShipSource{ID: id, Title: title, ListTool: "list"})
	}
	metadata, _ := json.Marshal(pluginapi.AlwaysShipScope{SourceIDs: ids, AllSessions: true, MaxBytes: 6000})
	object["capabilities"] = json.RawMessage(`[{"name":"context.always_ship","metadata":` + string(metadata) + `}]`)
	declared, _ := json.Marshal(sources)
	object["nanite"] = json.RawMessage(strings.Replace(string(object["nanite"]), `"registers":{`, `"registers":{"always_ship_sources":`+string(declared)+`,`, 1))
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return setupPlugin(t, string(raw), map[string]string{"bin/giphy": "#!/bin/sh\n", "envelopes/giphy-modal.schema.json": "{}"})
}

func TestInstallerRefusesAlwaysShipCapacityBeforeReviewOrCommit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		groups   [][]string
		proposed []string
	}{
		{"fifth owner", [][]string{{"A"}, {"B"}, {"C"}, {"D"}}, []string{"E"}},
		{"fifth source two owners", [][]string{{"A", "B", "C", "D"}}, []string{"E"}},
		{"normalized title", [][]string{{"Pinned_Context"}}, []string{"Pinned Context"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			options := BuildOptions{Extractor: &TarGzExtractor{}, Loader: &fakeLoader{}, PluginsRoot: filepath.Join(root, "plugins"), StagingRoot: filepath.Join(root, "staging"), Review: func(_ context.Context, review plugin.InstallReview, _ *plugin.InstallApproval) (string, error) {
				return review.Digest(), nil
			}}
			for i, titles := range tc.groups {
				owner := fmt.Sprintf("owner%d", i)
				installer, _ := NewInstaller(options)
				if _, err := installer.Install(context.Background(), &fakeSource{id: owner, handle: Handle{Kind: "directory", Path: admissionBundle(t, owner, titles...)}}); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			options.Review = func(_ context.Context, review plugin.InstallReview, _ *plugin.InstallApproval) (string, error) {
				called = true
				return review.Digest(), nil
			}
			installer, _ := NewInstaller(options)
			_, err := installer.Install(context.Background(), &fakeSource{id: "later", handle: Handle{Kind: "directory", Path: admissionBundle(t, "later", tc.proposed...)}})
			if err == nil || called || !strings.Contains(err.Error(), "always-ship") {
				t.Fatal("late runtime refusal instead of install admission", called, err)
			}
			if _, err := os.Stat(filepath.Join(options.PluginsRoot, "later")); !os.IsNotExist(err) {
				t.Fatal("refused bundle committed", err)
			}
			// Updating the existing owner replaces its reservation, not a fifth.
			installer, _ = NewInstaller(options)
			if _, err := installer.Install(context.Background(), &fakeSource{id: "owner0", handle: Handle{Kind: "directory", Path: admissionBundle(t, "owner0", tc.groups[0]...)}}); err != nil {
				t.Fatal("own update consumes extra reservation", err)
			}
		})
	}
}
