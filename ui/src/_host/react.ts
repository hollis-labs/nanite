// @ts-nocheck — React 19's `@types/react` uses `export =`, which TypeScript
// refuses to `export *` from. This file is a runtime entry chunk consumed by
// plugin bundles via the importmap; type-safety comes from the real
// `react` module at every import site.
//
// Host re-export entry for `react`. Plugin bundles resolve bare `react`
// imports to this chunk via the importmap injected into index.html, so the
// host and every plugin share one React instance.
import * as React from 'react'
export * from 'react'
export default React
