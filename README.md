# NANITE 

Nanite is a composable AI interface runtime.

It provides a unified workspace for interacting with models, agents, and tools—while remaining fully provider-agnostic.

Nanite handles sessions, context, and UI.  
Plugins extend behavior.  
Agents operate within it.

Out of the box, Nanite is a fast, minimal chat experience.  
With plugins, it becomes a programmable environment for building AI-powered systems, workflows, and interfaces.

## License & Branding

Nanite is open source under the MIT License.

You are free to use, modify, and build on Nanite for personal or commercial use.

The **Nanite name and branding are protected trademarks**.  
If you build on Nanite, you are encouraged to use attribution such as:

- “Built with Nanite”
- “Powered by Nanite”

See [TRADEMARK.md](./TRADEMARK.md) for details.

## Quick Start

```bash
go build ./cmd/nanite/
./nanite serve -port 8090 -db ./nanite.db -dev
```

See `docs/` for architecture and demo script.
