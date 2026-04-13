package subprocess

// The JSON-RPC wire protocol lives in github.com/hollis-labs/plugin-sdk/subprocess
// as of Track C.3. This file re-exports the types and constants under their
// original names so the rest of the nanite host code continues to reference
// them unqualified (RPCRequest / RPCResponse / MethodInit / ErrCodeNotFound / ...).
//
// Keep this shim thin — no new types should be added here. New wire types go
// in plugin-sdk; host-specific typed representations go in nanite/pkg/plugin.

import (
	sdksub "github.com/hollis-labs/plugin-sdk/subprocess"
)

// --- Wire primitives (type aliases) ---

type (
	RPCRequest  = sdksub.RPCRequest
	RPCResponse = sdksub.RPCResponse
	RPCError    = sdksub.RPCError
)

// --- Init handshake ---

type (
	InitParams = sdksub.InitParams
	HostInfo   = sdksub.HostInfo
	InitResult = sdksub.InitResult
)

// --- Load manifest ---
//
// As of plugin-sdk v0.2.0 (Track B.10) declarative registrations are
// yaml-authoritative: the host reads commands/slots/components/keybindings
// /config_schema/events/crud/dependencies from plugin.yaml and applies them
// directly. LoadResult is now an ack-only envelope; the only payload is
// SkippedRegistrations (runtime opt-outs the plugin surfaces back to the host).

type (
	LoadParams          = sdksub.LoadParams
	LoadResult          = sdksub.LoadResult
	SkippedRegistration = sdksub.SkippedRegistration
)

// --- Runtime request/response payloads ---

type (
	CommandExecParams = sdksub.CommandExecParams
	CommandExecResult = sdksub.CommandExecResult
	EventHandleParams = sdksub.EventHandleParams
	EventHandleResult = sdksub.EventHandleResult
	CRUDParams        = sdksub.CRUDParams
	CRUDResult        = sdksub.CRUDResult
	CRUDListResult    = sdksub.CRUDListResult
	HealthResult      = sdksub.HealthResult

	// B.10: new wire types for host->plugin methods beyond the
	// command/event/CRUD set. Stub-wired in Track B.10; downstream B.11/B.12
	// flesh out the surrounding policy + streaming.
	MCPCallRequest = sdksub.MCPCallRequest
	MCPCallResult  = sdksub.MCPCallResult
	HTTPRequest    = sdksub.HTTPRequest
	HTTPResponse   = sdksub.HTTPResponse
	MigrateParams  = sdksub.MigrateParams
	MigrateResult  = sdksub.MigrateResult
)

// --- Method constants ---

const (
	MethodInit           = sdksub.MethodInit
	MethodLoad           = sdksub.MethodLoad
	MethodUnload         = sdksub.MethodUnload
	MethodHealth         = sdksub.MethodHealth
	MethodCommandExecute = sdksub.MethodCommandExecute
	MethodEventHandle    = sdksub.MethodEventHandle
	MethodCRUDCreate     = sdksub.MethodCRUDCreate
	MethodCRUDRead       = sdksub.MethodCRUDRead
	MethodCRUDUpdate     = sdksub.MethodCRUDUpdate
	MethodCRUDDelete     = sdksub.MethodCRUDDelete
	MethodCRUDList       = sdksub.MethodCRUDList

	// B.10 additions.
	MethodMCPCallTool = sdksub.MethodMCPCallTool
	MethodHTTPHandle  = sdksub.MethodHTTPHandle
	MethodMigrate     = sdksub.MethodMigrate
)

// --- Error codes ---

const (
	ErrCodeParse          = sdksub.ErrCodeParse
	ErrCodeInvalidRequest = sdksub.ErrCodeInvalidRequest
	ErrCodeMethodNotFound = sdksub.ErrCodeMethodNotFound
	ErrCodeInvalidParams  = sdksub.ErrCodeInvalidParams
	ErrCodeInternal       = sdksub.ErrCodeInternal
	ErrCodeNotFound       = sdksub.ErrCodeNotFound
	ErrCodeConflict       = sdksub.ErrCodeConflict
	ErrCodeValidation     = sdksub.ErrCodeValidation
	ErrCodeCancelled      = sdksub.ErrCodeCancelled
)

// ProtocolVersion is the current wire-protocol version (see plugin-sdk).
const ProtocolVersion = sdksub.ProtocolVersion
