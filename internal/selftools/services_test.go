package selftools

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// Test-only adapters let package-local fixtures exercise the transport without
// importing service (which would create a cycle). Service tests cover wiring.
type testSkillReader struct{ *store.Store }

func (s testSkillReader) List(ctx context.Context) ([]store.Skill, error) { return s.ListSkills(ctx) }
func (s testSkillReader) Get(ctx context.Context, id string) (*store.Skill, error) {
	return s.GetSkill(ctx, id)
}

type testSessionReader struct{ *store.Store }

func (s testSessionReader) Get(ctx context.Context, id string) (*store.Session, error) {
	return s.GetSession(ctx, id)
}
func (s testSessionReader) GetByShortCode(ctx context.Context, id string) (*store.Session, error) {
	return s.GetSessionByShortCode(ctx, id)
}
func (s testSessionReader) ListMessagesPage(ctx context.Context, id string, limit, offset int) (*store.MessagePage, error) {
	return s.ListMessagesPaginated(ctx, id, limit, offset)
}

type testProcedureReader struct{ *store.Store }

func (s testProcedureReader) GetProcedure(ctx context.Context, id, name string) (*store.AgentProcedure, error) {
	return s.GetAgentProcedure(ctx, id, name)
}

type testHandoffService struct{ *store.Store }

func (s testHandoffService) Upsert(ctx context.Context, row store.HandoffStash) error {
	return s.UpsertHandoffStash(ctx, row)
}
func (s testHandoffService) Get(ctx context.Context, sessionID, id string) (store.HandoffStash, error) {
	return s.GetHandoffStash(ctx, sessionID, id)
}
func testReadServices(st *store.Store) ReadServices {
	if st == nil {
		return ReadServices{}
	}
	return ReadServices{Skills: testSkillReader{st}, Sessions: testSessionReader{st}, Procedures: testProcedureReader{st}, Handoffs: testHandoffService{st}}
}
func newTestSelfToolsTransport(st *store.Store) *SelfToolsTransport {
	return NewSelfToolsTransport(st, testReadServices(st), testWriteServices(st))
}

type testPinWriter struct{ *store.Store }

func (s testPinWriter) Create(ctx context.Context, pin store.PinnedContent) error {
	return s.CreatePinnedContent(ctx, pin)
}
func (s testPinWriter) Delete(ctx context.Context, id string) error {
	return s.DeletePinnedContent(ctx, id)
}

type testScheduleWriter struct{ *store.Store }

func (s testScheduleWriter) InsertPrepared(ctx context.Context, row store.AgentSchedule) error {
	return s.InsertAgentSchedule(ctx, row)
}
func testWriteServices(st *store.Store) WriteServices {
	if st == nil {
		return WriteServices{}
	}
	return WriteServices{Pins: testPinWriter{st}, Schedules: testScheduleWriter{st}, Membership: st, Dispatch: st, Events: st}
}

// fixtureStore is only for seeding and inspecting package-local test fixtures.
func fixtureStore(st *SelfToolsTransport) *store.Store {
	return st.Reads.Sessions.(testSessionReader).Store
}
