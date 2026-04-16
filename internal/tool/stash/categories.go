package stash

import "github.com/hollis-labs/nanite/internal/tool"

// registryView is the minimal surface of a broker registry that stash needs to
// classify tools by name. Defined at the point of consumption to avoid pulling
// in the broker package — the real broker.Registry satisfies this interface
// via its Get method.
type registryView interface {
	Get(name string) tool.Tool
}

// RegistryCategorizer returns a Categorizer backed by a broker registry. The
// registry is authoritative for tool categories; any tool missing from the
// registry (e.g., a provider-supplied ToolDefinition not yet registered)
// returns empty, which the stash buckets under CategoryOther.
func RegistryCategorizer(reg registryView) Categorizer {
	if reg == nil {
		return CategorizerFunc(func(string) string { return "" })
	}
	return CategorizerFunc(func(name string) string {
		t := reg.Get(name)
		if t == nil {
			return ""
		}
		return t.Category()
	})
}
