# Build a Nanite envelope component

Envelopes are structured cards embedded in chat messages. Their source of truth is `config/envelopes.yaml`; generated Go and TypeScript registries must stay in sync with that manifest.

## Wire format

Agents emit JSON in a `nanite-envelope` fence:

````markdown
```nanite-envelope
{
  "type": "example-card",
  "data": {
    "title": "Example"
  }
}
```
````

`type` selects a manifest-registered renderer. `data` must match that envelope's declared shape. Do not emit prose inside the fence or use a historical fence name.

## Add a core envelope

1. Add or update the type in `config/envelopes.yaml`.
2. Add the React component under `ui/src/components/chat/envelopes/`.
3. Keep the component's data props aligned with the manifest shape.
4. Run `npm run generate:plugins` from `ui/`.
5. Verify the generated registry is clean after a second generation run.
6. Exercise both the live SSE streaming path and persisted message reload path.

Never hand-edit generated registries. The manifest and component are authored inputs; generators own derived files.

## Component contract

An envelope renderer receives validated `data` and may receive an `onSendMessage` callback for a user action. It must render safely when optional fields are absent and give loading, success, and error feedback for interactive work. Treat envelope data as untrusted input: do not inject raw HTML and do not execute values as code.

Keep interactions narrow. An envelope should submit a clear user intent back through the normal chat path rather than bypassing server authorization or mutating unrelated state from the browser.

## Key source paths

| Purpose | Path |
|---|---|
| Manifest source of truth | `config/envelopes.yaml` |
| Envelope parser | `internal/chat/envelope.go` |
| React renderers | `ui/src/components/chat/envelopes/` |
| Message rendering | `ui/src/components/chat/ChatMessage.tsx` |
| Generator command | `ui/package.json` (`generate:plugins`) |

## Verification

Run focused Go parser tests and relevant UI tests, then `./scripts/check.sh`. Also confirm malformed JSON, unknown types, optional fields, streaming fragments, and persisted reload do not crash or silently render the wrong component.
