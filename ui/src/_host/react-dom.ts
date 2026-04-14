// @ts-nocheck — see _host/react.ts for the full explainer. Same CJS→ESM
// round-trip issue applies to react-dom (BLG-20260414-011); keep the
// explicit named list in sync with `Object.keys(require('react-dom'))`.
import * as ReactDOM from 'react-dom'

export {
  createPortal,
  flushSync,
  preconnect,
  prefetchDNS,
  preinit,
  preinitModule,
  preload,
  preloadModule,
  requestFormReset,
  unstable_batchedUpdates,
  useFormState,
  useFormStatus,
  version,
} from 'react-dom'

export default ReactDOM
