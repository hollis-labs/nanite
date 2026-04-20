// Package muxproxy wires a chat session to agent-mux subordinates.
// POC per CW-20260420-0047 — expected to be reverted or promoted wholesale.
package muxproxy

import (
	_ "github.com/chrispian/agent-mux/pkg/claudestream"
	_ "github.com/hollis-labs/go-agentmux-client"
)
