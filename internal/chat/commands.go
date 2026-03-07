package chat

// SlashCommand represents a slash command available in the chat UI.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// BuiltinCommands is the registry of built-in slash commands.
var BuiltinCommands = []SlashCommand{
	{Name: "mode", Description: "Switch agent mode", Category: "agent"},
	{Name: "architect", Description: "Switch to architect mode", Category: "agent"},
	{Name: "planner", Description: "Switch to planner mode", Category: "agent"},
	{Name: "writer", Description: "Switch to writer mode", Category: "agent"},
	{Name: "compact", Description: "Compact session context", Category: "session"},
	{Name: "clear", Description: "Clear session messages", Category: "session"},
	{Name: "new", Description: "Create new chat session", Category: "session"},
	{Name: "bookmark", Description: "Bookmark the last message", Category: "tools"},
	{Name: "help", Description: "Show available commands", Category: "help"},
}

// ListCommands returns all available slash commands.
func ListCommands() []SlashCommand {
	return BuiltinCommands
}
