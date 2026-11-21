# Module UI and verification

A module's `web/index.tsx` imports the page contract from `@hollis-labs/surface-page/sdk` and exports `views` satisfying ModuleWeb. ViewProps gives win, desk, and the scoped api. Compose controls from `@/components/ui/*`; follow `registry/README.md` and `docs/gui.md` in the target checkout.

Use Button for actions, Input/Textarea/TextEditor for entry, Field for labeled controls, Item/Table for collections, Badge/Progress for status and Empty for an empty state. Use the registry's dialogs and card renderers for decisions. Arrange with layout classes and theme tokens. Component className changes placement, not its visual style. No raw button/input/select/textarea, literal colors, or local overrides of installed components. Icons use lucide-react.

Change a component or variant in `registry/ui`, rebuild (`make ui-build`), sync (`node scripts/ui.mjs sync`) and add its gallery specimen. Never edit `web/src/components/ui` directly. Theme choices live in `registry/theme/surface.css`.

Expose the record and selection with desk.report; mark edits dirty and composing as appropriate. Use `useAnchor` for meaningful points an agent can indicate. For references use CiteText/CiteRef, and use shared marks against the stable ref. Views refresh on module events, including writes made by agents with no page open.

## Assembly and checks

Add a module to `apps/<app>/surface.yaml`; run `make generate` and include generated wiring. Do not hand-edit generated app files. A module with a web entry must be present in the generated web assembly too.

Use `module/moduletest` for behavior at the Host seam: state transition, tool result, emitted event, cancellation/stale revision, and a delegate refused while the owner edits. A UI smoke check should exercise the actual workflow and inspect resulting state, not just count controls. A specialist smoke turn additionally needs a run-log record proving which specialist ran.

Repository checks include `make test vet lint`, `npm run typecheck`, `npm run lint`, registry build/verify, `make generate` with no generated drift, `make all`, npm pack-check, and the public-repo scan. Consult `.github/workflows/ci.yml` for the exact current pipeline. Use disposable `<APP>_DATA_DIR` for smoke tests; do not use a real conversation. Record which checks ran and their results rather than claiming CI from local validation.
