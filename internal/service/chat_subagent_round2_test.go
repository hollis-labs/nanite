package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	ctxpkg "github.com/hollis-labs/substrate/agent/context"
	messaging "github.com/hollis-labs/substrate/mesh/messaging/mailbox"
)

func TestSubagentZeroOwnerMultipleMessagesPreserveBaseBatch(t *testing.T) {
	pending := []messaging.Message{{ID: "one", FromAgentID: "first", Body: "ONE"}, {ID: "two", FromAgentID: "second", Body: "TWO"}}
	fake := &drainingSubagentInbox{fakeSubagentInbox: fakeSubagentInbox{msgs: pending}}
	result := &SlotAssemblyResult{Window: ctxpkg.NewContextWindow(200000, nil)}
	result.Window.SetContent(ctxpkg.SlotUserContext, "CORE")
	service := &chatServiceImpl{subagentInbox: fake}
	service.evaluateAndInjectSubagentResults(context.Background(), "s", "a", result)
	want := "CORE\n\n<system-reminder>\nSubagent result (from first): ONE\nSubagent result (from second): TWO\n</system-reminder>"
	if result.Window.Slot(ctxpkg.SlotUserContext).Content != want || !reflect.DeepEqual(fake.ackedIDs(), []string{"one", "two"}) {
		t.Fatal("zero-owner batch/Ack differs from base", result.Window.Slot(ctxpkg.SlotUserContext).Content, fake.ackedIDs())
	}
	if got := service.evaluateAndInjectSubagentResults(context.Background(), "s", "a", result); len(got) != 0 || len(fake.ackedIDs()) != 2 {
		t.Fatal("batch repeated", got)
	}
}

func TestSubagentCurrentFreeHeadroomDelivery(t *testing.T) {
	for _, size := range []int{100, 1300, 3000, 7000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			before := strings.Repeat("c", 6847) // core + heavy pins leave ~1151 bytes, not an empty 8 KB slot
			message := messaging.Message{ID: "result", FromAgentID: "worker", Body: strings.Repeat("R", size)}
			fake := &drainingSubagentInbox{fakeSubagentInbox: fakeSubagentInbox{msgs: []messaging.Message{message}}}
			service := &chatServiceImpl{subagentInbox: fake}
			assemble := func() *SlotAssemblyResult {
				w := ctxpkg.NewContextWindow(200000, nil)
				w.SetContent(ctxpkg.SlotUserContext, before)
				return &SlotAssemblyResult{Window: w, AlwaysShipActive: true}
			}
			result := assemble()
			service.evaluateAndInjectSubagentResults(context.Background(), "s", "a", result)
			expected := before + "\n\n" + formatSubagentResultInjection([]messaging.Message{message})
			if len(expected) > 8000 {
				expected = expected[:8000] + "\n[truncated]"
			}
			if result.Window.Slot(ctxpkg.SlotUserContext).Content != expected || !reflect.DeepEqual(fake.ackedIDs(), []string{"result"}) {
				t.Fatal("free-headroom truncation/Ack differs from base", size, fake.ackedIDs())
			}
			if got := service.evaluateAndInjectSubagentResults(context.Background(), "s", "a", assemble()); len(got) != 0 || len(fake.ackedIDs()) != 1 {
				t.Fatal("delivered result repeated", size)
			}
		})
	}
}

func TestSubagentHeadroomIsBytesIncludingSeparator(t *testing.T) {
	for _, tc := range []struct{ used, want int }{{0, 8000}, {2000, 5998}, {6847, 1151}, {8000, 0}} {
		slot := &ctxpkg.Slot{MaxTokens: 2000, Content: strings.Repeat("c", tc.used)}
		if got := subagentResultHeadroomBytes(slot); got != tc.want {
			t.Fatal("headroom billed bytes as tokens or omitted separator", tc.used, got, tc.want)
		}
	}
}

func TestRevokedAlwaysShipDoesNotTaxCoreOrChangeAckPolicy(t *testing.T) {
	for _, duringFetch := range []bool{false, true} {
		t.Run(fmt.Sprint(duringFetch), func(t *testing.T) {
			ctx := context.Background()
			db, err := storetest.New(t, ctx, t.TempDir()+"/db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.DB.Close() })
			session := &store.Session{ID: "s"}
			if err = db.CreateSession(ctx, session); err != nil {
				t.Fatal(err)
			}
			coreBytes := 7200
			if duringFetch {
				coreBytes = 4000
			}
			if err = db.SetSessionContextPrompt(ctx, "s", strings.Repeat("c", coreBytes)); err != nil {
				t.Fatal(err)
			}
			registry := NewPluginAlwaysShipSources()
			revoked := !duringFetch
			calls := 0
			addAlwaysShip(t, registry, "pins", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
				calls++
				revoked = true
				return alwaysShipReply("MUST NOT SHIP"), nil
			}), func(context.Context) error {
				if revoked {
					return errors.New("revoked")
				}
				return nil
			})
			agent := &store.AgentProfile{ID: "a"}
			base, err := NewContextService(ContextServiceConfig{Client: chat.NewContextClient(db), SlotStasher: fakeArtifactStasher{}}).AssembleSlots(ctx, session, agent, nil, "", 200000, "")
			if err != nil {
				t.Fatal(err)
			}
			actual, err := NewContextService(ContextServiceConfig{Client: chat.NewContextClient(db), AlwaysShip: registry, SlotStasher: fakeArtifactStasher{}}).AssembleSlots(ctx, session, agent, nil, "", 200000, "")
			if err != nil {
				t.Fatal(err)
			}
			pending := []messaging.Message{{ID: "one", FromAgentID: "worker", Body: "ONE"}, {ID: "two", FromAgentID: "worker", Body: "TWO"}}
			baselineInbox := &drainingSubagentInbox{fakeSubagentInbox: fakeSubagentInbox{msgs: pending}}
			actualInbox := &drainingSubagentInbox{fakeSubagentInbox: fakeSubagentInbox{msgs: pending}}
			(&chatServiceImpl{subagentInbox: baselineInbox}).evaluateAndInjectSubagentResults(ctx, "s", "a", base)
			(&chatServiceImpl{subagentInbox: actualInbox}).evaluateAndInjectSubagentResults(ctx, "s", "a", actual)
			if actual.Window.Slot(ctxpkg.SlotUserContext).Content != base.Window.Slot(ctxpkg.SlotUserContext).Content || !reflect.DeepEqual(actualInbox.ackedIDs(), baselineInbox.ackedIDs()) || actual.AlwaysShipActive {
				t.Fatal("revoked lease taxed core or changed base batch/Ack policy", actual.AlwaysShipActive, actualInbox.ackedIDs(), baselineInbox.ackedIDs())
			}
			wantCalls := 0
			if duringFetch {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("wrong revoked fetch count", calls, wantCalls)
			}
		})
	}
}
