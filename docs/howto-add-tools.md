# How to Add MCP Tools to Mentat Chat

This guide covers adding new tools to the built-in MCP tool transports in Mentat Chat.

## Transport Types

### DevToolsTransport (`internal/mcp/dev_tools.go`)
- Path-scoped tools that operate on the local filesystem
- All file operations are restricted to `AllowedPaths` directories
- Tool names use the `dev_` prefix (e.g. `dev_read`, `dev_bash`)
- Has access to the `isAllowed(path)` helper for path validation

### GeneralToolsTransport (`internal/mcp/general_tools.go`)
- Utility tools with no path scoping
- Tool names have no prefix (e.g. `web_fetch`, `hash`)
- For tools that don't need filesystem access

### When to Create a New Transport
Create a new transport file implementing the `Transport` interface when:
- Tools need different scoping rules (e.g. database-scoped)
- Tools share a common dependency (e.g. an API client)
- The existing transports don't fit conceptually

## The Transport Interface

```go
type Transport interface {
    ListTools(ctx context.Context) ([]Tool, error)
    CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error)
}
```

## Step-by-Step: Adding a Tool

### 1. Define the Tool Schema in `ListTools`

Add a `Tool` entry to the slice returned by `ListTools`:

```go
{
    Name:        "dev_example",
    Description: "One-line description of what this tool does.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "param1": map[string]any{"type": "string", "description": "What this param does"},
            "param2": map[string]any{"type": "integer", "description": "Optional with default"},
        },
        "required": []string{"param1"},
    },
},
```

### 2. Add a Case in `CallTool`

```go
case "dev_example":
    return d.callExample(args)
```

### 3. Implement the Handler

```go
func (d *DevToolsTransport) callExample(args map[string]any) (*ToolResult, error) {
    // Extract and validate params.
    param1, _ := args["param1"].(string)
    if param1 == "" {
        return errorResult("param1 is required"), nil
    }

    // For dev tools: validate path scoping.
    if err := d.isAllowed(param1); err != nil {
        return errorResult(err.Error()), nil
    }

    // Use intArg helper for optional integers with defaults.
    param2 := intArg(args, "param2", 10)

    // Do the work...
    result := fmt.Sprintf("processed %s with %d", param1, param2)

    // Return success or error.
    return textResult(result), nil
}
```

### 4. Register the Transport (if new)

In `cmd/mentat-chat/main.go`, add the server:

```go
myTransport := mcp.NewMyTransport()
mcpManager.AddServer("my-transport", myTransport)
```

### 5. Write Tests

Create or update `internal/mcp/<transport>_test.go`:

```go
func TestDevExample_Success(t *testing.T) {
    dt, dir := tempDevTools(t)
    // Set up test files if needed...
    result, err := dt.CallTool(context.Background(), "dev_example", map[string]any{
        "param1": filepath.Join(dir, "test.txt"),
    })
    if err != nil {
        t.Fatal(err)
    }
    if result.IsError {
        t.Fatalf("unexpected error: %s", result.Content[0].Text)
    }
    // Assert on result.Content[0].Text
}
```

### 6. Verify

```bash
go build ./...
go test ./internal/mcp/... -v
```

## Naming Conventions

| Transport | Prefix | Examples |
|-----------|--------|---------|
| DevToolsTransport | `dev_` | `dev_read`, `dev_bash`, `dev_glob` |
| GeneralToolsTransport | none | `web_fetch`, `hash`, `math_eval` |

## Helper Functions

Available in `dev_tools.go` (shared across the package):

- `textResult(text string) *ToolResult` — success result with text content
- `errorResult(msg string) *ToolResult` — error result
- `intArg(args map[string]any, key string, def int) int` — extract optional integer with default

## Security Considerations

- **Path scoping**: Always use `d.isAllowed(path)` before any file operation in dev tools
- **Input validation**: Validate required params early, return `errorResult` for bad input
- **Resource limits**: Set timeouts, limit output size, cap iterations
- **No eval**: Never execute arbitrary code from tool inputs; use safe parsers

## Worked Example: Adding a `dev_stat` Tool

A complete example of adding a tool that returns file metadata.

**1. Tool definition** (in `ListTools`):
```go
{
    Name:        "dev_stat",
    Description: "Get file metadata: size, permissions, modification time.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "path": map[string]any{"type": "string", "description": "Absolute file path"},
        },
        "required": []string{"path"},
    },
},
```

**2. Dispatch** (in `CallTool`):
```go
case "dev_stat":
    return d.callStat(args)
```

**3. Handler**:
```go
func (d *DevToolsTransport) callStat(args map[string]any) (*ToolResult, error) {
    path, _ := args["path"].(string)
    if path == "" {
        return errorResult("path is required"), nil
    }
    if err := d.isAllowed(path); err != nil {
        return errorResult(err.Error()), nil
    }

    info, err := os.Stat(path)
    if err != nil {
        return errorResult(fmt.Sprintf("stat: %v", err)), nil
    }

    result := fmt.Sprintf("Name: %s\nSize: %d bytes\nMode: %s\nModified: %s",
        info.Name(), info.Size(), info.Mode(), info.ModTime().Format(time.RFC3339))
    return textResult(result), nil
}
```

**4. Test**:
```go
func TestDevStat_Success(t *testing.T) {
    dt, dir := tempDevTools(t)
    path := filepath.Join(dir, "test.txt")
    os.WriteFile(path, []byte("hello"), 0o644)

    result, err := dt.CallTool(context.Background(), "dev_stat", map[string]any{
        "path": path,
    })
    if err != nil {
        t.Fatal(err)
    }
    if result.IsError {
        t.Fatalf("unexpected error: %s", result.Content[0].Text)
    }
    if !strings.Contains(result.Content[0].Text, "5 bytes") {
        t.Error("expected size in output")
    }
}
```
