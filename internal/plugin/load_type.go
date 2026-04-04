package plugin

// LoadTypeResolver resolves the effective loadType for a tool by walking the
// override chain: session > project > agent > manifest default.
//
// Each layer is a map of tool name (bare, not prefixed) → LoadType. A zero
// value ("") means "no opinion at this layer, fall through to the next."
type LoadTypeResolver struct {
	// Layers ordered from highest to lowest priority.
	// Typically: [session, project, agent, manifest].
	layers []LoadTypeLayer
}

// LoadTypeLayer is a named set of tool-level loadType overrides.
type LoadTypeLayer struct {
	Name      string              // e.g. "session", "project", "agent", "manifest"
	Overrides map[string]LoadType // tool name → loadType
}

// NewLoadTypeResolver creates a resolver with the given layers.
// Layers should be ordered from highest to lowest priority.
func NewLoadTypeResolver(layers ...LoadTypeLayer) *LoadTypeResolver {
	return &LoadTypeResolver{layers: layers}
}

// Resolve returns the effective loadType for a tool. It walks the layers from
// highest to lowest priority and returns the first non-empty value. If no layer
// has an opinion, returns LoadTypeAuto.
func (r *LoadTypeResolver) Resolve(toolName string) LoadType {
	for _, layer := range r.layers {
		if lt, ok := layer.Overrides[toolName]; ok && lt != "" {
			return lt
		}
	}
	return LoadTypeAuto
}

// ResolveWithSource returns the effective loadType and which layer it came from.
func (r *LoadTypeResolver) ResolveWithSource(toolName string) (LoadType, string) {
	for _, layer := range r.layers {
		if lt, ok := layer.Overrides[toolName]; ok && lt != "" {
			return lt, layer.Name
		}
	}
	return LoadTypeAuto, "default"
}

// IsToolEnabled returns true if the tool should be loaded (i.e. not opt-in,
// or explicitly enabled via an override).
func (r *LoadTypeResolver) IsToolEnabled(toolName string) bool {
	return !r.Resolve(toolName).IsOptIn()
}

// ToolLoadPreferences is a serializable map of tool name → loadType,
// stored in user settings or session metadata.
type ToolLoadPreferences map[string]LoadType

// ToLayer converts preferences into a LoadTypeLayer with the given name.
func (p ToolLoadPreferences) ToLayer(name string) LoadTypeLayer {
	overrides := make(map[string]LoadType, len(p))
	for k, v := range p {
		overrides[k] = v
	}
	return LoadTypeLayer{Name: name, Overrides: overrides}
}
