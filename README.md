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
lefthook install                # required once per clone — installs git hooks
go build ./cmd/nanite/
./nanite serve -port 8090 -db ./nanite.db -dev
```

`lefthook.yml` is tracked, but a tracked config installs no git hooks by itself.
Skip `lefthook install` and the pre-commit format/migration checks and the
`main`-scoped pre-push test run simply never execute. Confirm it took:

```bash
find .git/hooks -type f ! -name '*.sample'   # expect pre-commit and pre-push
```

Commit time is formatting only, by design. Whole-repo analysis — `go vet`,
scoped `golangci-lint`, and the test suite — lives in `./scripts/check.sh`,
the landing check; run it when a feature lands, not on every commit.

See `docs/` for architecture and demo script. Agent Workflows specifically are
sequenced exclusively by the embedded `go-workflow v0.1.0` module and are
documented in [the engine boundary](docs/architecture/workflow-engine.md) and
[the operator runbook](docs/workflow-operations.md).

Embedded-memory and external MCP operators upgrading to Tesseract v0.10 should
follow [Nanite's Tesseract v0.10 migration guide](docs/tesseract-v0.10-migration.md).
