package agent

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestAutoApproveOpenWarning(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			var logs bytes.Buffer
			a, err := Open(t.Context(), Options{Dir: agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: auto-test\n"), GlobalSkillsDir: "-", Provider: fake.New(), AutoApprove: auto, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = a.Close() }()
			n := strings.Count(logs.String(), "auto mode is on")
			if (auto && n != 1) || (!auto && n != 0) {
				t.Fatalf("auto=%v log=%s", auto, logs.String())
			}
		})
	}
}
