package authored

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/internal/warden"
	"github.com/hollis-labs/nanite/internal/warden/wardentest"
)

func TestIdentityWiresFailFastGrants(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			srv := wardentest.NewFake(t, "parent")
			file := filepath.Join(t.TempDir(), "credential")
			if err := tokenfile.Write(file, "parent"); err != nil {
				t.Fatal(err)
			}
			id, err := ParseIdentity([]byte("principal: agent:fixture\nwarden:\n  url: "+srv.URL+"\n  credential_file: "+file+"\n"), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			callbacks := 0
			src, err := NewWardenSource(id, nil, nil, func(warden.Waiting) { callbacks++ }, nil, auto)
			if err != nil {
				t.Fatal(err)
			}
			_, err = src.Token(warden.WithConversation(t.Context(), "chat"), "fixture", nil)
			if err == nil || !strings.Contains(err.Error(), "request") {
				t.Fatalf("error %v", err)
			}
			_, waiting := warden.AsMissingGrant(err)
			if waiting == auto {
				t.Fatalf("auto=%v waiting=%v", auto, waiting)
			}
			want := 1
			if auto {
				want = 0
			}
			if callbacks != want {
				t.Fatalf("callbacks=%d want=%d", callbacks, want)
			}
		})
	}
}
