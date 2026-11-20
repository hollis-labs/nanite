package agent

import (
	"context"
	"fmt"
	"strings"

	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

// Reasons a background job ended because Nanite stopped, not because of
// what the job did: Close ran while it ran (status "cancelled"), or the
// process exited and the next Open found it still running (status
// "error"). A job view's "reason" carries them; a host that started the job
// may start it again.
const (
	JobReasonShutdown    = nanitestore.JobReasonShutdown
	JobReasonProcessExit = nanitestore.JobReasonProcessExit
)

// hostStatePrefix keeps the host's state apart from Nanite's own settings
// in the same table.
const hostStatePrefix = "host."

func hostKey(key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("nanite: host state key is empty")
	}
	return hostStatePrefix + key, nil
}

// HostState is a value the host stored on the conversation with
// SetHostState, "" when there is none. Host state outlives a restart and is
// not part of the transcript: the model never sees it.
func (c *Conversation) HostState(ctx context.Context, key string) (string, error) {
	k, err := hostKey(key)
	if err != nil {
		return "", err
	}
	return c.agent.store.Setting(ctx, c.id, k)
}

// SetHostState stores a value for the host on the conversation, replacing
// what was there; "" removes it.
func (c *Conversation) SetHostState(ctx context.Context, key, value string) error {
	k, err := hostKey(key)
	if err != nil {
		return err
	}
	return c.agent.store.SetSetting(ctx, c.id, k, value)
}

// HostState is an agent-wide value the host stored with SetHostState, ""
// when there is none.
func (a *Agent) HostState(ctx context.Context, key string) (string, error) {
	k, err := hostKey(key)
	if err != nil {
		return "", err
	}
	return a.store.Setting(ctx, "", k)
}

// SetHostState stores an agent-wide value for the host; "" removes it.
func (a *Agent) SetHostState(ctx context.Context, key, value string) error {
	k, err := hostKey(key)
	if err != nil {
		return err
	}
	return a.store.SetSetting(ctx, "", k, value)
}

// ConversationHostStates is every conversation's host state under key, by
// conversation ID, so a host can find what to restore after a restart.
func (a *Agent) ConversationHostStates(ctx context.Context, key string) (map[string]string, error) {
	k, err := hostKey(key)
	if err != nil {
		return nil, err
	}
	all, err := a.store.SettingsByKey(ctx, k)
	if err != nil {
		return nil, err
	}
	delete(all, "")
	return all, nil
}
