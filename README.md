# CONDUIT

Agent-agnostic, multi-agent chat harness for [Fragments Engine](https://github.com/hollis-labs).

CONDUIT is infrastructure — it routes messages, manages sessions, assembles context, and renders UI. Agents do the thinking. Any Special Agent profile can be loaded.

## Quick Start

```bash
go build ./cmd/conduit/
./conduit serve -port 8090 -db ./conduit.db -dev
```

See `docs/` for architecture and demo script.
