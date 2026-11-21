package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/tools"
	"github.com/hollis-labs/nanite/toolregistry"
)

func (x *execution) toolInstall() tools.Tool {
	return tools.Tool{Name: "tool_install", Description: "Install a registry tool package into this agent's tools directory and pin its digest. Existing pins cannot change. Installation does not approve execution; the package is considered on the next turn.", InputSchema: json.RawMessage(`{"type":"object","properties":{"ref":{"type":"string"}},"required":["ref"],"additionalProperties":false}`), Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
		var in struct {
			Ref string `json:"ref"`
		}
		if err := json.Unmarshal(call.Input, &in); err != nil {
			return tools.Result{}, err
		}
		r, err := toolregistry.Install(ctx, *x.engine.cfg.ToolRegistry, x.engine.profileDir(ctx), in.Ref, x.engine.cfg.ToolPackageEnv...)
		if err != nil {
			return tools.Result{}, err
		}
		return tools.Result{Text: fmt.Sprintf("Installed %s (%s). Execution still requires package approval or AutoApprove; available from the next turn.", r.Ref(), r.Digest)}, nil
	}}
}
