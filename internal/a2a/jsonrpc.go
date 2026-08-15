package a2a

// ── JSON-RPC 2.0 Types ────────────────────────────────────────────────────────
//
// Per https://www.jsonrpc.org/specification
// A2A v1.0 uses JSON-RPC 2.0 transport for Task methods.

// JSONRPCVersion is the protocol version string for JSON-RPC 2.0.
const JSONRPCVersion = "2.0"

// JSONRPCRequest represents a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      any    `json:"id"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
	ID      any           `json:"id"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	JSONRPCParseError     = -32700
	JSONRPCInvalidRequest = -32600
	JSONRPCMethodNotFound = -32601
	JSONRPCInvalidParams  = -32602
	JSONRPCInternalError  = -32603
)

// A2A-specific error codes (application-defined range: -32000 to -32099).
const (
	// ErrTaskNotFound: TaskID does not exist.
	ErrTaskNotFound = -32000
	// ErrTaskRejected: Task submission rejected (validation failure, target not found).
	ErrTaskRejected = -32001
	// ErrTargetNotFound: Target workflow/instance does not exist.
	ErrTargetNotFound = -32002
	// ErrInvalidTarget: Target is malformed or not addressable.
	ErrInvalidTarget = -32003
)

// NewJSONRPCRequest constructs a JSON-RPC 2.0 request.
func NewJSONRPCRequest(method string, params any, id any) JSONRPCRequest {
	return JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  params,
		ID:      id,
	}
}

// NewJSONRPCResponse constructs a JSON-RPC 2.0 success response.
func NewJSONRPCResponse(result any, id any) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result:  result,
		ID:      id,
	}
}

// NewJSONRPCErrorResponse constructs a JSON-RPC 2.0 error response.
func NewJSONRPCErrorResponse(code int, message string, data any, id any) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}
}
