# Nanite

Nanite runs an authored agent directory as a local CLI or an embedded Go library. It assembles per-turn context, calls model and MCP providers, and keeps a durable conversation transcript.

```sh
go install github.com/hollis-labs/nanite/cmd/nanite@latest
nanite init ./my-agent
cd ./my-agent
nanite
nanite -p 'Summarize this workspace' --output json
```

Configure an Anthropic or OpenAI provider before the first model turn. See [agent directory and gateway settings](docs/agent-directory.md). For a checkout of the rebuild, use the [development setup](docs/validation.md) until its dependencies are released.

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

[Embedding walkthrough](docs/embedding.md) · [Architecture](docs/architecture.md) · [Validation](docs/validation.md)
