// Package chatrunner runs a Nanite agent behind the conversation handler in
// github.com/hollis-labs/libs/agent/chat. It implements chat.Runner over the
// embedding package: each chat conversation is one Nanite conversation, the
// agent's events become chatstream events, the host's tools run in the
// host's process, and a tool call that needs approval becomes an
// approval.request that waits for an answer on the wire.
//
//	r, err := chatrunner.Open(ctx, chatrunner.Options{
//		Agent: agent.Options{Dir: "agents/desk"},
//		Tools: []chatrunner.Tool{...},
//	})
//	svc, err := chat.New(chat.Config{Runner: r, ...})
//
// See docs/embedding.md for the levels a host tool can have and what the
// adapter does with the turn's context.
package chatrunner
