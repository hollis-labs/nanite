# Surface Module conventions pack

Pack version: 1.0.0
Source repository: https://github.com/hollis-labs/surface
Source commit: b69d288c6494aa51bdf156ae4f5d5830b4df38bc

Copy `skills/design-surface-workflow/` and the four `context/surface-*.md` files into the workflow designer agent directory, preserving paths. This pack does not replace agent.md or runtime configuration. Keep the pack version and source revision with redistributed copies. Update all pack files together; bump patch for corrections, minor for compatible additions, major for changed conventions requiring consumer changes.

The contract and current code take precedence over the historical status checklist in docs/ade-framework.md, which includes planned features and old configuration references. This version documents embedded profiles; refresh after the module-agent binding lands before claiming external-agent hosting support.

## Source fingerprints (SHA-256)

- `docs/writing-a-module.md`: `5c41d426e8f7e3accfe74eedca02eea8b13fad11d270ee9a9646bec09d35c65f`
- `docs/architecture.md`: `b58e4672f5d1c116af3a0cb2015cf3bd53df149139181623217ef7e48e1004d9`
- `docs/ade-framework.md`: `19233bcefda146c1a6de06b635d1f8c48463e95495d431e92029e2ecbc23e526`
- `docs/gui.md`: `a5881517fee34b051d25e98183e3fb8540a5db440104e058b99c913484b703b9`
- `registry/README.md`: `e15f5563ef4ec1674d5d9159086350c0478e1a96e8a2b093bca0f48c02a79f21`
- `module/module.go`: `88da912a055ecc07758fee4da52b357c6ead92bfad4ce8d0940b69d9b46f8f75`
- `module/contract.go`: `5692ab196f1a40296cd8f503bb62304701b18c29b0ded77d1c0c8cef8672876d`
- `module/host.go`: `c61da3eab942e31062475be8d5a511968c96f8241440a85d24348b1c244ca767`
- `module/refs.go`: `a1f268cd7ed9959335d5791191bde6361253cd901e5b9d56d7e20daf244b81aa`
- `modules/ops/ops.go`: `9cf981783233e48b61d386bab9848da3477742ff598f12e1cb1a55bfc4c660c0`
- `modules/ops/tools.go`: `a87e7fc46d311a6ad4adcaea8c74322eaed511121747a751aa7985fa0d2a86b1`
- `modules/ops/cite.go`: `62b0c66d80f73c47ea1f9a98b871b78195239b3137baf1125df28cf667334ba1`
- `modules/workbench/workbench.go`: `ead9c6f7e0a8428f54c5e9880fd2de18296bc1aec294ae71ae05ec445d181ae1`
- `modules/workbench/web/index.tsx`: `c08ba37c9750a09a06fd58945514d23e1687f530ce77f123f5868bc7c853778f`
