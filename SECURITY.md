# Security policy

## Supported versions

Nanite is pre-1.0 software, built in public and not yet officially released. Security fixes land on `main` and ship in a new `0.x` release. Older versions do not get backported fixes.

## Report a vulnerability

Do not put an exploit, token, credential, transcript or other sensitive material in a public issue.

Report privately through GitHub's vulnerability-reporting form on the repository's **Security** tab. If the form is not available, contact a maintainer privately through a channel listed on the Hollis Labs organization or a maintainer's profile. Include:

- the affected version or commit and your operating system
- the part involved: the CLI, the `agent` package, `chatrunner`, a provider adapter, the MCP client, or a built-in tool
- reproduction steps and the security impact
- a safe way to contact you to coordinate disclosure

Maintainers acknowledge private reports, investigate them, and coordinate disclosure. Response times are best effort.

## Trust model

Nanite runs a model-driven agent on your machine, with your privileges.

- **No server.** Nanite is a CLI and a Go library. It does not listen on a network port. An application that embeds it and serves it, for example behind the `libs/agent/chat` handler, is responsible for its own authentication and exposure.
- **Tools run as you.** The file tools are confined to the workspace roots, and protected paths are refused, including credential directories and Nanite's own `.nanite/` data. An approved `bash` command, a background job, or a tool from an MCP server you configured runs with your user's privileges and can reach anything you can reach. Nanite has no OS sandbox or network egress control yet. The shell denylist only guards against obvious accidents.
- **Permissions are the control.** Every tool call goes through the permission rules in `permissions.yaml` and, when one is configured, the host's approval gate. Review the rules before you give an agent a mode that runs write or shell tools without asking.
- **Agent directories are code.** `agent.md`, `host.yaml`, `permissions.yaml`, `.mcp.json` and skills decide what the agent can do and which commands start MCP servers. Only open agent directories you trust.
- **Secrets.** Provider keys are read from files named by `key_ref`. The values of secrets Nanite resolves (provider and MCP credentials) are redacted from tool input and output, events and logs. The environment fallback (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`) is off unless you enable it.
- **Transcripts hold your data.** `.nanite/transcripts/` and `.nanite/nanite.db` contain the conversation and every tool result. Only known secret values are redacted; anything else a tool reads is stored as is. Keep agent directories out of public repositories; `nanite init` writes a `.gitignore` that excludes `.nanite/`.

See [Architecture: known limitations](docs/architecture.md#known-limitations) for more detail.
