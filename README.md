# Mentat Chat

Keyboard-first, multi-agent chat client for Fragments Engine.

## What is this?

Mentat Chat is a conversational command center for working with AI agents across multiple contexts — project management, business strategy, writing, personal planning. It's the GUI replacement for the CLI-based Mentat meta-agent.

## Architecture

- **Backend:** Go 1.25, SQLite (modernc), REST + SSE
- **Frontend:** React 19, TypeScript, Tailwind 4, shadcn/ui, TipTap
- **Tools:** MCP integration with Volon, Cortex, Hadron
- **Deployment:** Single binary (embedded SPA) or Docker

## Development

```bash
# Prerequisites: Go 1.25+, Node 22+, npm

# Start Go server (with hot reload)
air

# Start React dev server (in another terminal)
cd ui && npm run dev

# Production build
make build
```

## Key Concepts

- **Workspaces** scope context (Fragments Engine, Writing, Personal)
- **Sessions** are individual conversations with persistent history
- **Mentat** is a cognitive agent — it plans and delegates, never executes directly
- **Worker agents** handle tool calls and execution via MCP
- **Modes** modify agent behavior without switching identity
- **Envelopes** enable structured interaction (proposals, questions, approvals)

## License

MIT - Hollis Labs
