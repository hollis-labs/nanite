package mcpbridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CandidateOperationKeyMeta is an internal candidate correlation field. Its
// presence grants no permission or idempotency; the host execution owner must
// validate it and own actual mutation receipts and commit rules.
const CandidateOperationKeyMeta = "nanite/candidate_operation_key"

type StdioLimits struct {
	MaxLineBytes      int
	MaxCancelDuration time.Duration
}

// Stdio is an unregistered internal adapter, not the public `nanite mcp` entry
// point. It receives an already scoped Proxy and owns no bootstrap, environment
// token, actor enrollment, database, plugin process or core-tool fallback.
type Stdio struct {
	proxy  *Proxy
	server *sdk.Server
	limits StdioLimits
}

type candidateListResult struct {
	sdk.ResultBase
	NextCursor string     `json:"nextCursor,omitempty"`
	Tools      []mcp.Tool `json:"tools"`
}

func NewStdio(ctx context.Context, proxy *Proxy, limits StdioLimits) (*Stdio, error) {
	if proxy == nil {
		return nil, ErrAuthorityUnavailable
	}
	if limits.MaxLineBytes <= 0 || limits.MaxCancelDuration <= 0 {
		return nil, errors.New("explicit positive candidate stdio limits are required")
	}
	catalog, err := proxy.List(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		return nil, ErrTargetUnavailable
	}
	var pinned Catalog
	if json.Unmarshal(raw, &pinned) != nil {
		return nil, ErrTargetUnavailable
	}
	adapter := &Stdio{proxy: proxy, limits: limits, server: sdk.NewServer(&sdk.Implementation{Name: "nanite-internal-mcp-candidate", Version: "1"}, nil)}
	tools := make(map[string]mcp.Tool, len(pinned.Tools))
	for _, definition := range pinned.Tools {
		if _, duplicate := tools[definition.Name]; duplicate || definition.InputSchema == nil || definition.InputSchema["type"] != "object" {
			return nil, ErrTargetUnavailable
		}
		tools[definition.Name] = definition.Tool
		adapter.server.AddTool(&sdk.Tool{Name: definition.Name, Description: definition.Description, InputSchema: definition.InputSchema}, adapter.handler(definition))
	}
	// SDK typed annotations concretize absent boolean hints. Its supported custom
	// ResultBase projection preserves optional manifest fields on the wire without
	// changing the SDK, process-global flags or JSON-RPC framing/dispatch.
	adapter.server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, request)
			}
			current, err := proxy.List(ctx)
			if err != nil {
				return nil, err
			}
			currentRaw, encodeErr := json.Marshal(current)
			// Actor-policy filtering may change without a global definition
			// revision. Refuse any changed projection, rather than expose a
			// previously visible declaration from the pinned session snapshot.
			if encodeErr != nil || current.Revision != pinned.Revision || !bytes.Equal(raw, currentRaw) {
				return nil, subprocess.ErrStaleBinding
			}
			result, err := next(ctx, method, request)
			if err != nil {
				return nil, err
			}
			listed, ok := result.(*sdk.ListToolsResult)
			if !ok {
				return nil, ErrTargetUnavailable
			}
			out := &candidateListResult{NextCursor: listed.NextCursor, Tools: make([]mcp.Tool, 0, len(listed.Tools))}
			out.SetMeta(listed.GetMeta())
			for _, tool := range listed.Tools {
				definition, ok := tools[tool.Name]
				if !ok {
					return nil, ErrTargetUnavailable
				}
				out.Tools = append(out.Tools, definition)
			}
			return out, nil
		}
	})
	return adapter, nil
}

func (s *Stdio) Run(ctx context.Context) error {
	if s == nil || s.server == nil {
		return ErrAuthorityUnavailable
	}
	return s.server.Run(ctx, &sdk.StdioTransport{MaxLineLength: s.limits.MaxLineBytes})
}

func (s *Stdio) handler(tool ToolDefinition) sdk.ToolHandler {
	return func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if request == nil || request.Params == nil {
			return nil, ErrTargetUnavailable
		}
		var args map[string]any
		if len(request.Params.Arguments) != 0 && json.Unmarshal(request.Params.Arguments, &args) != nil {
			return nil, errors.New("invalid candidate tool arguments")
		}
		operationKey := ""
		if value, ok := request.Params.GetMeta()[CandidateOperationKeyMeta]; ok {
			key, valid := value.(string)
			if !valid || len(key) > 256 {
				return nil, errors.New("invalid candidate operation key")
			}
			operationKey = key
		}
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return nil, ErrTargetUnavailable
		}
		requestID := hex.EncodeToString(id)
		result, err := s.proxy.Call(ctx, CallRequest{Version: CandidateVersion, RequestID: requestID, Name: tool.Name, Binding: tool.Binding, Arguments: args, OperationKey: operationKey})
		if err != nil {
			if ctx.Err() != nil {
				// Explicit best-effort cancellation of this exact credential/request only.
				// This cannot prove rollback, known outcome or successful cancellation.
				cancelCtx, done := context.WithTimeout(context.WithoutCancel(ctx), s.limits.MaxCancelDuration)
				_, _ = s.proxy.Cancel(cancelCtx, requestID)
				done()
			}
			return nil, err
		}
		out := &sdk.CallToolResult{IsError: result.IsError, Content: make([]sdk.Content, 0, len(result.Content))}
		for _, content := range result.Content {
			// Accepted manifest transport currently returns text blocks, including
			// structured child output. Do not invent a lossy mapping for future kinds.
			if content.Type != "text" {
				return nil, ErrTargetUnavailable
			}
			out.Content = append(out.Content, &sdk.TextContent{Text: content.Text})
		}
		return out, nil
	}
}
