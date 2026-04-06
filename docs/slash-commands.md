# Slash Commands

Nanite provides a slash command system accessible from the chat composer. Type `/` to see available commands, or `@` to reference files.

## Built-in Commands

| Command | Category | Behavior |
|---------|----------|----------|
| `/new` | session | Create a new chat session |
| `/fork` | session | Fork current session with full message history |
| `/clone` | session | Clone session (empty, same settings) |
| `/bookmark` | session | Bookmark the last assistant message |
| `/compact` | session | Compact session context |
| `/agent` | agent | Switch primary agent (needs args) |
| `/model` | config | Switch model (needs args) |
| `/help` | help | Show formatted list of all available commands |

Commands with `(needs args)` expect the user to type additional text after the command name and send as a message.

## File References

Type `@` followed by a filename or path fragment to search workspace files. The autocomplete menu shows matching files and directories with fuzzy matching. Selecting a file inserts `@path/to/file` into the message, giving the agent a precise file reference.

- Searches the session's project `repo_path`, or the first project in the workspace, or cwd as fallback
- Skips `.git`, `node_modules`, `vendor`, `dist`, `build`, `__pycache__`, `.cache`, `.idea`, `.vscode`, `target`
- Hidden files/dirs (e.g. `.agentrc`, `.claude`, `.env`) are included
- Max depth: 8 levels, max results: 20
- 150ms debounce on keystroke

## Plugin Command Registration

Plugins can register slash commands via the `Host.RegisterCommand` method during `Load()`.

### Go Plugin Example

```go
func (p *MyPlugin) Load(host plugin.Host) error {
    // Type-assert to Nanite's Host for command registration.
    nh := host.(*nplugin.Host)
    return nh.RegisterCommand(nplugin.SlashCommandDef{
        Name:        "mycommand",
        Description: "Does something useful",
        Category:    "custom",
        Handler: func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) {
            // Execute the command server-side.
            // Return action + optional content.
            return map[string]interface{}{
                "action":  "message",   // "message" | "noop" | "error"
                "content": "Command executed successfully.",
            }, nil
        },
    })
}
```

### Handler Actions

The `action` field in the handler response tells the frontend what to do:

| Action | Behavior |
|--------|----------|
| `message` | Content is persisted as a system message in the session and displayed in the transcript |
| `noop` | Command executed silently, no visible feedback |
| `client` | Frontend handles the command (used by built-in commands like `/new`) |
| `error` | Content is shown as an error |

### SlashCommandDef Fields

```go
type SlashCommandDef struct {
    Name        string   // Command name (no leading slash)
    Description string   // Shown in the autocomplete menu
    Category    string   // Grouping in the menu (e.g. "session", "tools", "custom")
    Handler     func(ctx context.Context, sessionID, args string) (map[string]interface{}, error)
}
```

If `Handler` is nil, the command is client-side only — the frontend must handle it.

## API Endpoints

### `GET /api/commands`

Returns all registered commands (built-in + plugin).

```json
[
  { "name": "help", "description": "Show available commands", "category": "help", "source": "builtin" },
  { "name": "mycommand", "description": "Does something", "category": "custom", "source": "plugin" }
]
```

### `POST /api/commands/execute`

Execute a command server-side.

```json
{ "name": "help", "session_id": "abc-123", "args": "" }
```

Response:
```json
{ "action": "message", "content": "**Available Commands**\n...", "message_id": "def-456" }
```

### `GET /api/autocomplete/files?q=<query>&session_id=<id>`

Returns matching files for the `@` file reference autocomplete.

```json
[
  { "path": "internal/api/commands.go", "name": "commands.go", "is_dir": false, "size": 2048, "mod_time": "2026-03-28T10:00:00Z" },
  { "path": "internal/chat", "name": "chat", "is_dir": true, "size": 0, "mod_time": "2026-03-28T09:00:00Z" }
]
```
