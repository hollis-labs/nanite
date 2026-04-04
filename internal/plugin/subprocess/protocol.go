package subprocess

import (
	"encoding/json"

	"github.com/hollis-labs/plugin"
)

// JSON-RPC protocol for subprocess plugins.
//
// Communication is host-initiated: the host sends JSON-RPC requests over the
// subprocess's stdin, and reads responses from stdout. The subprocess never
// initiates requests — all registrations are declarative via the load response.

// --- JSON-RPC wire types ---

// RPCRequest is a JSON-RPC 2.0 request sent from host to plugin.
type RPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`  // 0 for notifications
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// RPCResponse is a JSON-RPC 2.0 response from plugin to host.
type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

// --- Method constants ---

const (
	// Lifecycle methods
	MethodInit   = "plugin/init"
	MethodLoad   = "plugin/load"
	MethodUnload = "plugin/unload"
	MethodHealth = "plugin/health"

	// Runtime methods (host → plugin)
	MethodCommandExecute = "command/execute"
	MethodEventHandle    = "event/handle"
	MethodCRUDCreate     = "crud/create"
	MethodCRUDRead       = "crud/read"
	MethodCRUDUpdate     = "crud/update"
	MethodCRUDDelete     = "crud/delete"
	MethodCRUDList       = "crud/list"
)

// Standard JSON-RPC error codes.
const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603

	// Application-level error codes (plugin-specific).
	ErrCodeNotFound   = -32000
	ErrCodeConflict   = -32001
	ErrCodeValidation = -32002
	ErrCodeCancelled  = -32003 // pre-hook cancellation
)

// --- Init handshake ---

// InitParams is sent by the host during plugin/init.
type InitParams struct {
	PluginDir string            `json:"plugin_dir"`
	Config    map[string]string `json:"config"`    // resolved config values
	HostInfo  HostInfo          `json:"host_info"` // host capabilities
}

// HostInfo describes the host environment to the plugin.
type HostInfo struct {
	Version  string `json:"version"`  // nanite version
	Protocol int    `json:"protocol"` // protocol version (1)
}

// InitResult is returned by the plugin in response to plugin/init.
type InitResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Protocol    int    `json:"protocol"` // protocol version the plugin supports
}

// --- Load registration manifest ---

// LoadParams is sent by the host during plugin/load (currently empty, reserved).
type LoadParams struct{}

// LoadResult is the registration manifest returned by the plugin during plugin/load.
// The host translates each section into the corresponding Register* calls.
type LoadResult struct {
	Dependencies []string             `json:"dependencies,omitempty"`
	Commands     []CommandRegistration `json:"commands,omitempty"`
	Slots        []plugin.UISlotEntry  `json:"slots,omitempty"`
	Components   []ComponentRegistration `json:"components,omitempty"`
	Keybindings  []plugin.KeybindingDef `json:"keybindings,omitempty"`
	ConfigSchema []plugin.ConfigFieldDef `json:"config_schema,omitempty"`
	EventSubscriptions []string         `json:"event_subscriptions,omitempty"`
	CRUDResources      []string         `json:"crud_resources,omitempty"` // resource type names for CRUD
}

// CommandRegistration is the wire representation of a slash command.
// The Handler field from SlashCommandDef can't be serialized, so the host
// creates a proxy handler that calls command/execute over JSON-RPC.
type CommandRegistration struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Category    string             `json:"category"`
	Args        []plugin.CommandArg `json:"args,omitempty"`
	Permission  string             `json:"required_permission,omitempty"`
}

// ComponentRegistration is the wire representation of a UIComponent.
// The Handler field is omitted — subprocess plugins serve UI via the
// frontend ESM loader (Phase 7 frontend), not via server-side handlers.
type ComponentRegistration struct {
	ID          string                 `json:"id"`
	Type        plugin.UIComponentType `json:"type"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Props       map[string]interface{} `json:"props,omitempty"`
}

// --- Runtime request/response types ---

// CommandExecParams is sent to the plugin for command/execute.
type CommandExecParams struct {
	Name      string `json:"name"`
	SessionID string `json:"session_id"`
	Args      string `json:"args"`
}

// CommandExecResult is returned by the plugin for command/execute.
type CommandExecResult struct {
	Action  string `json:"action"`            // "message", "noop", "error"
	Content string `json:"content,omitempty"`
}

// EventHandleParams is sent to the plugin for event/handle.
type EventHandleParams struct {
	Type      string                 `json:"type"`
	Source    string                 `json:"source"`
	Data      map[string]interface{} `json:"data"`
	SessionID string                 `json:"session_id,omitempty"`
	PreHook   bool                   `json:"pre_hook"` // true if host expects cancel/allow response
}

// EventHandleResult is returned by the plugin for event/handle.
type EventHandleResult struct {
	Cancel bool   `json:"cancel,omitempty"` // true to cancel a pre-hook action
	Reason string `json:"reason,omitempty"`
}

// CRUDParams is sent for all crud/* methods.
type CRUDParams struct {
	ResourceType string                 `json:"resource_type"`
	ID           string                 `json:"id,omitempty"`      // for read/update/delete
	Data         map[string]interface{} `json:"data,omitempty"`    // for create/update
	Filters      map[string]interface{} `json:"filters,omitempty"` // for list
}

// CRUDResult is returned for crud/create, crud/read, crud/update.
type CRUDResult struct {
	Data json.RawMessage `json:"data"`
}

// CRUDListResult is returned for crud/list.
type CRUDListResult struct {
	Items []json.RawMessage `json:"items"`
}

// HealthResult is returned by plugin/health.
type HealthResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// ProtocolVersion is the current protocol version.
const ProtocolVersion = 1
