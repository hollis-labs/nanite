# Working in any directory

Run `nanite` in a directory that is not an agent directory, without `-C`, and it works the way
`claude` and `codex` do: the directory is the **workspace**, and the agent is Nanite's global
**home**.

```sh
cd ~/code/my-app
nanite                                   # the terminal
nanite -p "Why does the build fail?"     # one prompt
nanite -p --continue "And now?"          # the latest session in this workspace
```

An agent directory (one with `agent.md` and `host.yaml`) still runs as before, and so does
`-C <dir>` (also `--agent-dir <dir>`). See [Agent directory](agent-directory.md).

## The home

The home is `$NANITE_HOME`, by default `~/.nanite`. It is an agent directory: `agent.md`,
`host.yaml`, `.mcp.json`, `permissions.yaml`, `skills/` and the rest work as they do in any agent
directory. The first time Nanite needs it, it writes the files the home lacks (`agent.md`, Nanite's
built-in base, and `host.yaml`) and says so on stderr. It never overwrites a file you have.

Put your own standing instructions in `~/.nanite/AGENTS.md`. You need not touch `agent.md`.

The file tools work in the workspace (and any `--add-dir`), not in the home: the agent cannot
change its own definition, permissions or other sessions' state.

## Instructions

The agent reads, in this order, each one more specific than the last:

1. `agent.md`'s instructions (the Agent slot): the home's own, or Nanite's built-in base.
2. `~/.nanite/AGENTS.md`.
3. For each directory from the outermost down to the workspace, its `AGENTS.md`, or its
   `CLAUDE.md` when it has no `AGENTS.md`. The walk starts below your home directory (`$HOME`
   itself is not read) when the workspace is under it, and at `/` when it is not.
4. `--append-system-prompt` and `--append-system-prompt-file`.

Files are read when a session starts and are at most 1 MiB each.

## Project settings

A directory between `$HOME` and the workspace can hold:

| File | What it sets |
| --- | --- |
| `.nanite/settings.json` | `model`, `permissions` (`allow`, `ask`, `deny` in claude's rule syntax, `defaultMode`, `additionalDirectories`) and `env`: the same keys as `--settings` |
| `.mcp.json` | MCP servers, in claude's shape |
| `.nanite/skills/<name>/SKILL.md` | skills |

`~/.nanite/settings.json` is the global settings file. The closest directory wins: settings are
applied home first, then from the outermost directory in, so a nearer `model` or `defaultMode`
replaces a farther one, while permission rules and additional directories add up (deny, then ask,
then allow, as always). A nearer `.mcp.json` server replaces one of the same name, and the home's
`.mcp.json` comes under all of them. The command line comes last: `--model`, `--permission-mode`,
`--settings`, `--mcp-config` (with `--strict-mcp-config`, only these servers) and the rest win
over the files.

## Sessions

Each session keeps its state in its own directory,
`~/.nanite/projects/<workspace>/<session id>/` (the workspace path with `/` and other separators
turned into `-`): its conversation, traces and event log. A session is one conversation whose id
is the session id.

- A plain run starts a new session.
- `--continue` picks up the workspace's most recent session (a new one if it has none).
- `--resume <id>` and `--session-id <id>` name one; `--resume` fails for an id this workspace has
  no session for, and `--session-id` creates it.
- Several sessions in one workspace can run at once.

Sessions do not share memory yet: history search and notes are per session. A shared per-project
store is planned.

`nanite stop --all` applies to agent-directory runs; stop a workspace session with Ctrl-C or, run
headless, an interrupt.
