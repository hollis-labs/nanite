# Build a Nanite envelope component

Envelopes are structured cards embedded in chat messages. Core envelope definitions are owned by the pinned [`github.com/hollis-labs/go-envelopes`](https://github.com/hollis-labs/go-envelopes) module: its `manifest/envelopes.yaml`, `manifest/envelopes.schema.json`, and `manifest/schemas/` tree are the source of truth. Nanite consumes that released module; it does not maintain a second local core manifest.

## Wire format

Agents emit JSON in a `nanite-envelope` fence:

````markdown
```nanite-envelope
{
  "kind": "content",
  "version": 1,
  "type": "info-card",
  "data": {
    "title": "Example",
    "body": "The released schema requires both fields."
  }
}
```
````

`kind` and `version` are required; current envelopes use a non-empty semantic kind and wire version `1`. `type` selects a registered renderer, and `data` must match that envelope's declared shape. Do not emit prose inside the fence or use a historical fence name.

## Add a core envelope

1. Add the definition and schema to the go-envelopes module's `manifest/envelopes.yaml` and `manifest/schemas/`, validate it against `manifest/envelopes.schema.json`, then release that module.
2. Bump Nanite's pinned `github.com/hollis-labs/go-envelopes` version.
3. Check out the same released go-envelopes tag at the repository's expected sibling path, `../../libs/go-envelopes`; both frontend generators read that module-owned source tree rather than the Go module cache.
4. Add the React component under `ui/src/components/chat/envelopes/` and keep its data props aligned with the released schema. Put the normal `component`, `export`, and `props` mapping in the upstream manifest; use `CORE_OVERRIDES` only for an intentional Nanite-only deviation.
5. Run `make generate-envelopes`; `scripts/generate-envelope-types.mjs` consumes the sibling checkout's `manifest/schemas/`. Run `npm run generate:plugins` from `ui/`; `scripts/generate-plugin-imports.mjs` consumes its `manifest/envelopes.yaml` and owns `ui/src/generated/plugin-envelopes.ts`.
6. Verify both generators are clean on a second run, then exercise the live SSE streaming and persisted-message reload paths.

Never hand-edit generated registries. The manifest and component are authored inputs; generators own derived files.

## Component contract

An envelope renderer receives validated `data` and may receive an `onSendMessage` callback for a user action. It must render safely when optional fields are absent and give loading, success, and error feedback for interactive work. Treat envelope data as untrusted input: do not inject raw HTML and do not execute values as code.

Keep interactions narrow. An envelope should submit a clear user intent back through the normal chat path rather than bypassing server authorization or mutating unrelated state from the browser.

## Key source paths

| Purpose | Path |
|---|---|
| Core manifest source of truth | go-envelopes `manifest/envelopes.yaml` |
| Core manifest schema | go-envelopes `manifest/envelopes.schema.json` |
| Envelope parser | `internal/chat/envelope.go` |
| React renderers | `ui/src/components/chat/envelopes/` |
| Message rendering | `ui/src/components/chat/ChatMessage.tsx` |
| Generator input checkout | `../../libs/go-envelopes` at the same released tag as `go.mod` |
| Core type generator | `scripts/generate-envelope-types.mjs` (`make generate-envelopes`) |
| Renderer registry generator | `scripts/generate-plugin-imports.mjs` (`ui/package.json` `generate:plugins`) |
| Generated renderer registry | `ui/src/generated/plugin-envelopes.ts` |

## Verification

Run focused Go parser tests and relevant UI tests, then `./scripts/check.sh`. Also confirm malformed JSON, unknown types, optional fields, streaming fragments, and persisted reload do not crash or silently render the wrong component.
