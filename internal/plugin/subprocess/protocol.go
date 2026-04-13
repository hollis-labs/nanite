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

type (
	LoadParams            = sdksub.LoadParams
	LoadResult            = sdksub.LoadResult
	CommandRegistration   = sdksub.CommandRegistration
	ComponentRegistration = sdksub.ComponentRegistration
	UISlotEntry           = sdksub.UISlotEntry
	KeybindingDef         = sdksub.KeybindingDef
	CommandArg            = sdksub.CommandArg
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
