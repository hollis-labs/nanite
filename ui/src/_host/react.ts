// @ts-nocheck — React 19's `@types/react` uses `export =`, which TypeScript
// refuses to `export *` from. This file is a runtime entry chunk consumed by
// plugin bundles via the importmap; type-safety comes from the real
// `react` module at every import site.
//
// Host re-export entry for `react`. Plugin bundles resolve bare `react`
// imports to this chunk via the importmap injected into index.html, so the
// host and every plugin share one React instance.
//
// React 19 ships CJS-only (no `module` field, no ESM `./index.js`). In Vite
// dev the package goes through the CJS→ESM dep-optimization layer and the
// resulting namespace does not round-trip named exports through a plain
// `export * from 'react'` in the browser — dynamically imported plugin
// bundles that do `import { forwardRef } from 'react'` fail with
// "does not provide an export named 'forwardRef'" (BLG-20260414-011).
//
// The workaround is an explicit named re-export list so the shim's module
// record advertises every React runtime name. Keep this in sync with
// `Object.keys(require('react'))` on version bumps — missing a name here
// will break any plugin that imports it.
import * as React from 'react'

export {
  Activity,
  Children,
  Component,
  Fragment,
  Profiler,
  PureComponent,
  StrictMode,
  Suspense,
  act,
  cache,
  cacheSignal,
  captureOwnerStack,
  cloneElement,
  createContext,
  createElement,
  createRef,
  forwardRef,
  isValidElement,
  lazy,
  memo,
  startTransition,
  use,
  useActionState,
  useCallback,
  useContext,
  useDebugValue,
  useDeferredValue,
  useEffect,
  useEffectEvent,
  useId,
  useImperativeHandle,
  useInsertionEffect,
  useLayoutEffect,
  useMemo,
  useOptimistic,
  useReducer,
  useRef,
  useState,
  useSyncExternalStore,
  useTransition,
  version,
} from 'react'

export default React
