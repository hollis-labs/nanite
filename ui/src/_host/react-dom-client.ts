// @ts-nocheck — see _host/react.ts. BLG-20260414-011 — explicit named
// re-export list keeps `createRoot` / `hydrateRoot` reachable for plugin
// bundles loaded through the host importmap.
export {
  createRoot,
  hydrateRoot,
  version,
} from 'react-dom/client'
