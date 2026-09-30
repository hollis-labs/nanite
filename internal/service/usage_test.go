package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// toolTokenStub answers the two reads SessionToolTokens makes. UsageStore is
// embedded nil: any other UsageStore call panics.
type toolTokenStub struct {
	UsageStore
	summary    *store.SessionToolTokenSummary
	summaryErr error
	eventCalls int
}

func (s *toolTokenStub) GetSessionToolTokenSummary(context.Context, string) (*store.SessionToolTokenSummary, error) {
	return s.summary, s.summaryErr
}

func (s *toolTokenStub) CountSessionToolCalls(context.Context, string) int {
	return s.eventCalls
}

func TestUsageSessionToolTokens(t *testing.T) {
	cases := []struct {
		name string
		stub *toolTokenStub
		want ToolTokenUsage
	}{
		{
			name: "recorded tool calls use actual tokens",
			stub: &toolTokenStub{
				summary:    &store.SessionToolTokenSummary{TotalInputTokens: 300, TotalOutputTokens: 45, TotalToolCalls: 4},
				eventCalls: 9,
			},
			want: ToolTokenUsage{Calls: 4, Tokens: 345},
		},
		{
			name: "summary error falls back to the event-log estimate",
			stub: &toolTokenStub{summaryErr: errors.New("db gone"), eventCalls: 3},
			want: ToolTokenUsage{Calls: 3, Tokens: 150, Estimated: true},
		},
		{
			name: "no recorded tool calls falls back to the event-log estimate",
			stub: &toolTokenStub{summary: &store.SessionToolTokenSummary{}, eventCalls: 2},
			want: ToolTokenUsage{Calls: 2, Tokens: 100, Estimated: true},
		},
		{
			name: "no tool calls either way",
			stub: &toolTokenStub{summary: &store.SessionToolTokenSummary{}},
			want: ToolTokenUsage{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewUsageService(tc.stub, tc.stub)
			if got := svc.SessionToolTokens(context.Background(), "s1"); got != tc.want {
				t.Fatalf("SessionToolTokens = %+v, want %+v", got, tc.want)
			}
		})
	}
}
