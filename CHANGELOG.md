# Changelog

All notable user-facing changes land here. This file starts with the
2026-05-01 release notes for the pre-release sprint; earlier history
lives in the git log.

## Unreleased

### Breaking

- **User config moved to XDG-compliant location** (CW-20260430-0010). The
  user-level chat-harness config is now read from
  `$XDG_CONFIG_HOME/nanite/config.yaml` (default
  `~/.config/nanite/config.yaml`). The legacy `~/.nanite/nanite.yaml` path
  is no longer read — there is no compatibility shim, no deprecation log,
  and no automated migration. Pre-release migration is manual:

  ```sh
  mkdir -p ~/.config/nanite
  cp ~/.nanite/nanite.yaml ~/.config/nanite/config.yaml
  ```

  If `XDG_CONFIG_HOME` is set in your environment, substitute its value
  for `~/.config`. The `~/.nanite/` directory is preserved for non-config
  files (skills, roles, agents, plugin data) and is unaffected by this
  change. Project-level `./nanite.yaml` is unchanged.
