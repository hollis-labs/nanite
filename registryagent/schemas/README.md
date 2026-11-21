These JSON Schemas are copied byte-for-byte from
`hollis-labs/tools`, commit `ff67fdc0`, `registry/contracts/schemas/`.
The agent schema is refreshed byte-for-byte from commit
`e938bd62a100d2d67659b7e2d05f54b5cf134dc9` (tools PR #73), which adds
`tool-package` to the accepted tool-reference kinds.
The `*-registry.json` variants include the agency-owned materialization fields.
The original schemas are MIT-licensed; see the upstream repository license.
Do not relax a schema locally to make a fixture pass. Refresh it from the
Registry contract when the upstream contract changes.

The tool-package schema is copied byte-for-byte from tools commit
`27519536219c937a23ec29e8c85a36b7e3dd3058`.
