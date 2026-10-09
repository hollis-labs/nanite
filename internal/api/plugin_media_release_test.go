package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdkplugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/ui-go/envelopes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service"
)

type mediaEnvelopeSink struct {
	mu        sync.Mutex
	sessions  []string
	envelopes []sdkplugin.EnvelopeOut
}

func (s *mediaEnvelopeSink) Deliver(session string, envelopes []sdkplugin.EnvelopeOut) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, session)
	s.envelopes = append(s.envelopes, envelopes...)
	return true
}

// Set NANITE_MEDIA_TEST_BUNDLES to a directory containing the externally
// verified giphy/v0.2.0 and oembed/v0.2.0 Linux release directories.
func TestMediaPublishedReleaseAdoption(t *testing.T) {
	published := os.Getenv("NANITE_MEDIA_TEST_BUNDLES")
	if published == "" {
		t.Skip("requires verified giphy and oembed v0.2.0 releases")
	}
	ctx := context.Background()
	st := newSeededStore(t)
	root := t.TempDir()
	for _, id := range []string{"giphy", "oembed"} {
		source, err := os.OpenRoot(filepath.Join(published, id)) // #nosec G703 -- opt-in integration fixture selects externally verified release directories.
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = source.Close() }()
		destination := filepath.Join(root, id)
		if err = os.Mkdir(destination, 0700); err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenRoot(destination)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = output.Close() }()
		if err = fs.WalkDir(source.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return output.MkdirAll(relative, 0700)
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			if !info.Mode().IsRegular() {
				return fs.ErrInvalid
			}
			raw, readErr := source.ReadFile(relative)
			if readErr != nil {
				return readErr
			}
			return output.WriteFile(relative, raw, info.Mode().Perm())
		}); err != nil {
			t.Fatal(err)
		}
		review, err := naniteplugin.BuildInstallReview(ctx, destination)
		if err != nil {
			t.Fatal(err)
		}
		if review.Version != "0.2.0" {
			t.Fatal("requires published v0.2.0")
		}
		if err = naniteplugin.SaveInstallApproval(root, review, review.Digest()); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("url") != "https://fixture.example/post/42" {
			t.Error("provider URL changed")
		}
		_, _ = w.Write([]byte(`{"type":"video","title":"Published preview","html":"<script>untrusted()</script>"}`))
	}))
	defer provider.Close()
	config, err := json.Marshal([]map[string]string{{"name": "Fixture", "pattern": "^https://fixture.example/post/", "endpoint": provider.URL + "/?url={url}"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertPluginSettings(ctx, "oembed", map[string]any{"oembed_extra_providers": string(config)}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIPHY_API_KEY", "")
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("media-release-test"))
	host.SetStore(st)
	host.SetEnvelopeRegistry(envelopes.NewRegistry())
	sink := &mediaEnvelopeSink{}
	host.SetEnvelopeConsumer(sink)
	manager := mcp.NewManager()
	host.SetMCPRegistrar(service.NewPluginToolRegistrar(manager, st))
	commands := chat.NewCommandRegistry()
	host.SetCommandRegistry(commands)
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	load := func() {
		t.Helper()
		loaded, failures := naniteplugin.LoadDiscovered(host, discovered)
		if len(loaded) != 2 || len(failures) != 0 {
			t.Fatalf("load: %v %v", loaded, failures)
		}
	}
	load()
	t.Cleanup(func() { _ = host.UnloadPlugin("giphy"); _ = host.UnloadPlugin("oembed") })
	result, err := commands.Execute(ctx, "giphy", "session-one", "cats")
	if err != nil || !strings.Contains(result.Content, "giphy-modal") {
		t.Fatalf("published command: %#v %v", result, err)
	}
	tool, err := manager.ExecuteTool(mcp.WithSessionID(ctx, "session-one"), "giphy_search", map[string]any{"query": "cats"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(tool)
	if err != nil || !bytes.Contains(raw, []byte("gif_url")) {
		t.Fatalf("published tool: %s %v", raw, err)
	}
	event := sdkplugin.Event{Type: "message.sent", SessionID: "session-one", Data: map[string]any{"content": "https://fixture.example/post/42"}}
	for i := 0; i < 2; i++ {
		host.EmitEvent(event)
	}
	sink.mu.Lock()
	if len(sink.envelopes) != 3 || len(sink.sessions) != 3 || sink.sessions[0] != "session-one" || sink.envelopes[0].Type != "giphy-modal" || sink.envelopes[1].Type != "oembed-card" || sink.envelopes[1].Data["title"] != "Published preview" {
		t.Errorf("published event: %#v %#v", sink.sessions, sink.envelopes)
	}
	sink.mu.Unlock()
	if requests.Load() != 1 {
		t.Fatal("preview cache missed")
	}
	if err = host.ValidatePluginEnvelope("giphy", "giphy-modal", map[string]any{"unexpected": true}); err == nil {
		t.Fatal("release schema not enforced")
	}
	if err = host.UnloadPlugin("giphy"); err != nil {
		t.Fatal(err)
	}
	if err = host.UnloadPlugin("oembed"); err != nil {
		t.Fatal(err)
	}
	if _, err = commands.Execute(ctx, "giphy", "session-one", "cats"); err == nil {
		t.Fatal("unload retained command")
	}
	host.EmitEvent(event)
	if requests.Load() != 1 {
		t.Fatal("unload retained event hook")
	}
	load()
	host.EmitEvent(event)
	if requests.Load() != 2 {
		t.Fatal("reload reused retired instance cache")
	}
}
