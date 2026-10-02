package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

type apiSubprocessFixture struct {
	id, name string
	identity json.RawMessage
}

func (p *apiSubprocessFixture) Init(_ context.Context, params sdkprocess.InitParams) (sdkprocess.InitResult, error) {
	p.identity = append(json.RawMessage(nil), params.Identity...)
	return sdkprocess.InitResult{ID: p.id, Name: p.name, Version: "1.0.0", Protocol: 1}, nil
}
func (p *apiSubprocessFixture) Load(context.Context) (sdkprocess.LoadResult, error) {
	return sdkprocess.LoadResult{}, nil
}
func (p *apiSubprocessFixture) Unload(context.Context) error { return nil }
func (p *apiSubprocessFixture) MCPCallTool(_ context.Context, request sdkprocess.MCPCallRequest) (sdkprocess.MCPCallResult, error) {
	if request.ToolName == "identity" {
		return sdkprocess.MCPCallResult{Content: p.identity}, nil
	}
	if request.ToolName == "exit" {
		os.Exit(3)
	}
	raw, _ := json.Marshal(map[string]int{"pid": os.Getpid()})
	return sdkprocess.MCPCallResult{Content: raw}, nil
}

func TestAPISubprocessChild(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--nanite-plugin-child" && i+2 < len(os.Args) {
			if err := sdkprocess.Serve(&apiSubprocessFixture{id: os.Args[i+1], name: os.Args[i+2]}); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
}

func writeAPIPluginBundle(t *testing.T, dir, id, name string, block pluginapi.Block) string {
	t.Helper()
	binary := filepath.Join(dir, "plugin")
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.Open(executable) // #nosec G304 -- copies this test executable from os.Executable, never a caller-controlled file.
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		target, err := os.OpenFile(binary, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0700) // #nosec G302 G304 -- executable subprocess fixture inside t.TempDir.
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(target, source)
		closeErr := target.Close()
		if copyErr != nil {
			t.Fatal(copyErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	for _, envelope := range block.Registers.Envelopes {
		if envelope.Schema != "" {
			path := filepath.Join(dir, envelope.Schema)
			if checkErr := os.MkdirAll(filepath.Dir(path), 0700); checkErr != nil {
				t.Fatal(checkErr)
			}
			if err := os.WriteFile(path, []byte(`{"type":"object"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	ext, err := pluginapi.EncodeBlock(block)
	if err != nil {
		t.Fatal(err)
	}
	declaration := manifest.Manifest{SchemaVersion: 2, ID: id, Name: name, Version: "1.0.0", Protocol: 1, Runtime: "subprocess", Entrypoint: manifest.Entrypoint{Command: "plugin", Args: []string{"-test.run=^TestAPISubprocessChild$", "--", "--nanite-plugin-child", id, name}}, Hosts: map[string]manifest.HostRange{"nanite": {Min: "0.1.0"}}, Nanite: ext}
	var raw strings.Builder
	if checkErr := manifest.Encode(&raw, declaration); checkErr != nil {
		t.Fatal(checkErr)
	}
	path := filepath.Join(dir, "plugin.yaml")
	if checkErr := os.WriteFile(path, []byte(raw.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	review, err := naniteplugin.BuildInstallReview(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := naniteplugin.SaveInstallApproval(filepath.Dir(dir), review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	return path
}

func TestUnloadPluginFromHost_UsesIdentifierNotName(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	pms := &pluginManagerState{pluginHost: host}
	dir := t.TempDir()
	path := writeAPIPluginBundle(t, dir, "mismatch-id", "Mismatch Display Name", pluginapi.Block{})
	if !pms.runPluginLoadIntoHost(path, dir) {
		t.Fatal("initial load failed")
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("mismatch-id") })
	if !pms.unloadPluginFromHost(path) {
		t.Fatal("unload failed")
	}
	if _, ok := host.GetPlugin("mismatch-id"); ok {
		t.Fatal("ID remains loaded")
	}
}

func TestRunPluginLoadIntoHost_ReloadTwice_IDNameMismatch(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	pms := &pluginManagerState{pluginHost: host}
	dir := t.TempDir()
	path := writeAPIPluginBundle(t, dir, "reload-mismatch-id", "Reload Display Name", pluginapi.Block{})
	for i := 0; i < 3; i++ {
		if !pms.runPluginLoadIntoHost(path, dir) {
			t.Fatalf("load cycle %d failed", i)
		}
		if !pms.unloadPluginFromHost(path) {
			t.Fatalf("unload cycle %d failed", i)
		}
	}
}

func TestRunPluginLoadIntoHost_RollbackUsesPluginID(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	if err := host.RegisterEnvelope(naniteplugin.EnvelopeRegistryEntry{Type: "already-owned", PluginID: "other", Component: "Other"}); err != nil {
		t.Fatal(err)
	}
	pms := &pluginManagerState{pluginHost: host}
	dir := t.TempDir()
	block := pluginapi.Block{UI: pluginapi.UI{Bundle: "ui.js"}, Registers: pluginapi.Registrations{Envelopes: []pluginapi.Envelope{{Type: "already-owned", Component: "Card", Version: 1, Schema: "schema.json"}}}}
	path := writeAPIPluginBundle(t, dir, "rollback-id", "Rollback Display Name", block)
	if pms.runPluginLoadIntoHost(path, dir) {
		t.Fatal("collision load succeeded")
	}
	if _, ok := host.GetPlugin("rollback-id"); ok {
		t.Fatal("failed load retained plugin")
	}
	path = writeAPIPluginBundle(t, dir, "rollback-id", "Rollback Display Name", pluginapi.Block{})
	if !pms.runPluginLoadIntoHost(path, dir) {
		t.Fatal("corrected load failed")
	}
	if !pms.unloadPluginFromHost(path) {
		t.Fatal("unload failed")
	}
}

func TestReviewedPluginRefusesChangedBundleOnLoadAndRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "reviewed.plugin")
	if checkErr := os.MkdirAll(directory, 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	path := writeAPIPluginBundle(t, directory, "reviewed.plugin", "Reviewed", pluginapi.Block{})
	parsed, err := naniteplugin.ParseManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	plugin, err := naniteplugin.NewSubprocessPluginFromManifest(context.Background(), naniteplugin.DiscoveredPlugin{Dir: directory, Manifest: parsed}, host)
	if err != nil {
		t.Fatal(err)
	}
	// The builder already accepted the bundle. Spawn must independently check it.
	asset := filepath.Join(directory, "added.txt")
	if checkErr := os.WriteFile(asset, []byte("changed"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := host.LoadPlugin(plugin); checkErr == nil {
		t.Fatal("changed bundle spawned after construction")
	}
	if checkErr := os.Remove(asset); checkErr != nil {
		t.Fatal(checkErr)
	}
	plugin, err = naniteplugin.NewSubprocessPluginFromManifest(context.Background(), naniteplugin.DiscoveredPlugin{Dir: directory, Manifest: parsed}, host)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := host.LoadPlugin(plugin); checkErr != nil {
		t.Fatal(checkErr)
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("reviewed.plugin") })
	if _, err := plugin.CallTool(context.Background(), &subprocess.MCPCallRequest{ToolName: "pid"}); err != nil {
		t.Fatal(err)
	}
	if checkErr := os.WriteFile(asset, []byte("changed"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	_, _ = plugin.CallTool(context.Background(), &subprocess.MCPCallRequest{ToolName: "exit"})
	// First supervised restart is due after one second; accepted bytes remain
	// changed throughout the attempt, so the original manager must have no child.
	time.Sleep(1500 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := plugin.CallTool(ctx, &subprocess.MCPCallRequest{ToolName: "pid"}); err == nil {
		t.Fatal("changed bundle restarted")
	}
}
