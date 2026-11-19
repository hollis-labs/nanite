// Package subagent defines the role and concurrency limits for child turns.
package subagent

import (
	"fmt"
	"strings"
	"sync"
)

type Start struct {
	Role, Task string
	Background bool
}

func (s Start) Validate() error {
	if s.Role != "worker" && s.Role != "planner" {
		return fmt.Errorf("subagent role must be worker or planner")
	}
	if strings.TrimSpace(s.Task) == "" {
		return fmt.Errorf("subagent task required")
	}
	return nil
}

func Prompt(role string) string {
	switch role {
	case "worker":
		return "You are a worker subagent. Complete the assigned task and report the result to your parent."
	case "planner":
		return "You are a planner subagent. Use read-only tools to gather context and return a concrete plan."
	default:
		return ""
	}
}

func AllowsTool(role, name string, readOnly bool) bool {
	if role == "planner" && !readOnly {
		return false
	}
	return role == "chat" || (!strings.HasPrefix(name, "subagent_") && name != "ask_user")
}

type Limit chan struct{}

func NewLimit() Limit { return make(Limit, 4) }
func (l Limit) Acquire() (func(), error) {
	select {
	case l <- struct{}{}:
		return sync.OnceFunc(func() { <-l }), nil
	default:
		return nil, fmt.Errorf("four subagents are already running")
	}
}
