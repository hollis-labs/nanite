# [Medium] `persistAdapterList` YAML round-trip loses comments and non-root document content silently

**Scope:** installer / adapter selection persistence
**Topic:** Correctness / data loss — user edits to `.nanite/config.yaml` are partially preserved
**Date:** 2026-04-10

## Problem

`persistAdapterList` uses `yaml.Node` round-tripping to insert or replace the `adapters:` key in `.nanite/config.yaml`, which the file comment claims preserves "key ordering and most formatting." In practice, the round-trip through `yaml.Unmarshal` → `yaml.Marshal` silently drops YAML anchors, reorders some comment attachments, and re-emits scalars with a normalized style (single-quoted vs. plain) different from the input. The code also only looks at `root.Content[0]` — if the config file happens to contain multiple YAML documents separated by `---`, only the first is preserved; the rest are silently discarded on the write-back.

The user is told, implicitly, that their `.nanite/config.yaml` is safe to hand-edit. The installer's documented `# Example:` stanza in the template even encourages it. But hand-editing works only as long as the user never runs `--reconfigure` or `--adapters` again, at which point some of their edits will be quietly rewritten.

This is not a security issue; it's a data-integrity issue that will manifest as "the installer mangled my config and lost my comments" bug reports during the beta.

## Evidence

```go
// internal/service/install/adapter_persist.go:L44-94
func persistAdapterList(path string, adapters []string) error {
    data, err := os.ReadFile(path)
    if err != nil {
        return fmt.Errorf("read config %s: %w", path, err)
    }

    var root yaml.Node
    if err := yaml.Unmarshal(data, &root); err != nil {
        return fmt.Errorf("parse config %s: %w", path, err)
    }

    // Root is a Document node; its first content is the mapping.
    if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
        return fmt.Errorf("config %s is not a yaml document", path)
    }
    mapping := root.Content[0]
    if mapping.Kind != yaml.MappingNode {
        return fmt.Errorf("config %s top-level is not a mapping", path)
    }
    ...
}
```

`yaml.Unmarshal` into a `yaml.Node` only walks one document. Go-yaml supports multi-document streams via `yaml.NewDecoder(r).Decode(&node)` in a loop — the single-call form used here stops at the first. If the input is multi-doc, the extra docs are silently dropped on the marshal back.

Line comments attached to a key are preserved *on that node*, but when `persistAdapterList` prepends a new `adapters:` key to the mapping (`newContent := append([]*yaml.Node{keyNode, adaptersValue}, mapping.Content...)`), the comment that was previously the head comment of the original first key is now an inline comment on `adapters` — not a head comment of the key it was written against.

```go
// internal/service/install/adapter_persist.go:L86-94
// Adapters key not present — insert it as the first key (for visibility).
keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "adapters"}
newContent := make([]*yaml.Node, 0, len(mapping.Content)+2)
newContent = append(newContent, keyNode, adaptersValue)
newContent = append(newContent, mapping.Content...)
mapping.Content = newContent
```

The template file has a comment on the first line:

```yaml
# internal/assets/framework/templates/nanite-config.yaml.tmpl:L1
# internal/assets/framework/templates/nanite-config.yaml.tmpl
# Project nanite config — {{.ProjectName}}
nanite_version: {{.FrameworkVersion}}

agents: {}
  # Example:
  # backend-dev:
  #   ...
```

On first install, `adapters:` does not exist, so the first run inserts it. The head comment on the document is likely to be re-emitted, but the head comment attached to `nanite_version:` (which was the first key) becomes a comment attached to the new `adapters:` key instead. On second install with `--reconfigure`, the same shuffle may happen again depending on how `yaml.v3` attaches foot/head comments to mapping values.

Also, the expensive idempotency contract of the installer — running twice produces the same output — is violated at the byte level for any config that had comments. The functional contract holds; the byte-level one does not.

### Test gap

`adapter_persist_test.go:L1-202` has a table-driven test suite that covers the expected behavioral outputs (adapters list is correct) but does NOT assert that:

- Leading comments on keys that were present before `adapters:` was inserted are preserved in place
- Multi-document input is either rejected or fully preserved
- Non-mapping root (e.g., `null`, a list, an empty document) is rejected with a clear error

Running the existing tests and diffing the input/output at the byte level would immediately show the drift.

## Impact

- **Who:** any user who hand-edits `.nanite/config.yaml` (the docs encourage this) and later runs `nanite install --reconfigure` or `nanite install --adapters=...`.
- **What:** comments drift to unexpected positions, anchors are lost, multi-doc files lose their tail documents. The user cannot tell from the file's new state what they lost.
- **Release impact:** beta users will hand-edit this file. This is a near-certain "the installer ate my comments" report. It is not a correctness bug for the adapter list itself — that part works — but it undermines trust in the installer's "safe to re-run" promise.

## Recommendation

Two options, pick one:

1. **(Recommended) Stop round-tripping YAML. Use a line-based editor for the single `adapters:` key.**
   The one key the installer manages is `adapters:`, at the top level, always a flow-style or block-style sequence of short strings. A line-based editor (grep for `^adapters:`, replace through the next non-indented key) preserves comments, anchors, multi-doc streams, and any other YAML feature the user wanted. The pattern is more brittle to malformed YAML but that failure mode is loud (the whole file breaks, user notices immediately). The current failure mode is silent.

   Sketch:

   ```go
   // Find the line range of the existing adapters: ... block.
   // Replace it in-place. If not present, insert after the first non-comment line.
   ```

2. **Keep YAML round-tripping but narrow the scope.** Use `yaml.NewDecoder` to support multi-doc; explicitly preserve `HeadComment` / `LineComment` / `FootComment` on reattached keys; reject (with a clear error) any file whose root is not exactly one mapping document; round-trip-test the result.

   This is more code than (1) but stays within the yaml.v3 API. It still can't perfectly preserve every formatting choice the user made (e.g., style of scalar quoting), but it gets the important cases right.

Either way, add tests that:

- Input file has `# comment on nanite_version:\nnanite_version: 1.0.0` and assert the comment is still attached to `nanite_version:` after `persistAdapterList` runs on a fresh (no-`adapters:`) config.
- Input file is multi-doc (`---\nfoo: bar\n---\nbaz: qux`) — assert the current behavior is either preserved or rejected, not silently truncated.
- Byte-level idempotency: `persistAdapterList(path, [claude]); persistAdapterList(path, [claude])` produces a file that is byte-identical on the second call.

The scope here is small: one file, ~100 lines, with clear test hooks. The reason this lands at Medium rather than Low is that the config file is the user's primary surface for customizing their project and the installer silently mutates it on every re-run.

## References

- `internal/service/install/adapter_persist.go:L12-106` — full persist implementation.
- `internal/service/install/adapter_persist_test.go` — existing test coverage, behavioral only.
- `internal/assets/framework/templates/nanite-config.yaml.tmpl` — the template the installer writes; note the head comments and the `agents:` block's inline docstring.
- go-yaml.v3 `Node` / comment handling docs: https://pkg.go.dev/gopkg.in/yaml.v3#Node
