# Nanite

Nanite runs an authored agent directory as a local CLI or an embedded Go library. It assembles per-turn context, calls model and MCP providers, and keeps a durable conversation transcript.

> **Status: building in public.** Nanite is under active development and not yet officially released. `0.x` versions can change the CLI, the agent-directory format and the Go API without notice or a migration path. Use it to follow along or experiment, not to depend on. See [SECURITY.md](SECURITY.md) for the trust model and how to report a vulnerability.

```sh
go install github.com/hollis-labs/nanite/cmd/nanite@latest
cd ~/code/my-project
nanite                                   # works in any directory, like claude
nanite -p 'Summarize this workspace' --output json
```

In a directory that is not an agent directory, Nanite's home (`~/.nanite`) is the agent and the
directory is the workspace; it reads `AGENTS.md` (or `CLAUDE.md`) walking up and the project's
`.nanite/settings.json` and `.mcp.json`. See [working in any directory](docs/workspace.md). For a
dedicated agent of your own, `nanite init ./my-agent` makes an agent directory; run `nanite` in it
or with `-C ./my-agent`.

Configure an Anthropic or OpenAI provider before the first model turn. See [agent directory and gateway settings](docs/agent-directory.md). See [validation](docs/validation.md) for building from a source checkout.

Embed the same agent with `github.com/hollis-labs/nanite/agent`. A host can supply its own provider, permission gate, tools, skills, and context.

```go
import "github.com/hollis-labs/nanite/agent"

func ask(ctx context.Context, dir, prompt string) (string, error) {
	a, err := agent.Open(ctx, agent.Options{Dir: dir})
	if err != nil { return "", err }
	defer a.Close()
	c, err := a.NewConversation(ctx)
	if err != nil { return "", err }
	run, err := c.Send(ctx, agent.Input{Text: prompt})
	if err != nil { return "", err }
	result, err := run.Wait()
	return result.Text, err
}
```

[Working in any directory](docs/workspace.md) · [Running headless, like claude -p](docs/headless.md) · [Registry agent materialization](docs/registry-agents.md) · [Embedding walkthrough](docs/embedding.md) · [Architecture](docs/architecture.md) · [Validation](docs/validation.md)
