package service

import "github.com/hollis-labs/nanite/internal/tool"

func isResultCacheTool(name string) bool { return tool.IsResultCacheTool(name) }

// isCacheExemptTool: see tool.IsCacheExemptTool, which owns the list so the
// self-tool HTTP proxy path applies the identical exemptions.
func isCacheExemptTool(name string) bool { return tool.IsCacheExemptTool(name) }
