/**
 * Dynamic ESM loader for plugin UI bundles.
 *
 * Plugins that ship a `ui/bundle.js` in their directory get served by the
 * backend at `GET /api/plugins/{name}/ui/bundle.js`. This module:
 *
 * 1. Checks if the bundle exists (HEAD request).
 * 2. Dynamically imports the ESM module.
 * 3. Calls the module's `register(api)` export with the plugin registry API.
 *
 * All errors are caught and logged — a broken plugin bundle never crashes the app.
 */
import {
  createRegistryAPI,
  markPluginLoaded,
  isPluginLoaded,
} from './plugin-loader'

/** Expected shape of a plugin UI bundle's exports. */
interface PluginUIModule {
  register: (api: ReturnType<typeof createRegistryAPI>) => void | Promise<void>
}

const BUNDLE_PATH = (name: string) => `/api/plugins/${name}/ui/bundle.js`
const LOAD_TIMEOUT_MS = 10_000

/**
 * Check if a plugin has a UI bundle available on the server.
 * Uses a HEAD request to avoid downloading the full bundle just to check.
 */
async function hasBundleAvailable(pluginName: string): Promise<boolean> {
  try {
    const res = await fetch(BUNDLE_PATH(pluginName), { method: 'HEAD' })
    return res.ok
  } catch {
    return false
  }
}

/**
 * Load a single plugin's UI bundle and call its register() function.
 * Returns true if the module was loaded and registered successfully.
 */
export async function loadPluginModule(pluginName: string): Promise<boolean> {
  if (isPluginLoaded(pluginName)) return true

  // Check availability first to avoid noisy console errors from failed imports.
  const available = await hasBundleAvailable(pluginName)
  if (!available) return false

  try {
    // Dynamic import with a timeout to prevent hanging.
    const modulePromise = import(
      /* @vite-ignore */ BUNDLE_PATH(pluginName)
    ) as Promise<PluginUIModule>

    let timer: ReturnType<typeof setTimeout> | undefined
    const timeoutPromise = new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(`Plugin "${pluginName}" UI bundle load timed out`)), LOAD_TIMEOUT_MS)
    })

    const mod = await Promise.race([modulePromise, timeoutPromise]).finally(() => clearTimeout(timer))

    if (typeof mod.register !== 'function') {
      console.warn(
        `[plugin-esm] Plugin "${pluginName}" bundle has no register() export — skipping`,
      )
      markPluginLoaded(pluginName)
      return false
    }

    // Create scoped registry API and call register.
    const api = createRegistryAPI(pluginName)
    await Promise.resolve(mod.register(api))

    markPluginLoaded(pluginName)
    return true
  } catch (err) {
    console.error(`[plugin-esm] Failed to load plugin "${pluginName}" UI bundle:`, err)
    // Mark as loaded (attempted) so we don't retry endlessly.
    markPluginLoaded(pluginName)
    return false
  }
}

/**
 * Load UI bundles for all given plugins. Loads concurrently.
 * Returns the names of plugins that loaded successfully.
 */
export async function loadAllPluginModules(
  pluginNames: string[],
): Promise<string[]> {
  const results = await Promise.allSettled(
    pluginNames.map(async (name) => {
      const ok = await loadPluginModule(name)
      return ok ? name : null
    }),
  )

  return results
    .filter(
      (r): r is PromiseFulfilledResult<string> =>
        r.status === 'fulfilled' && r.value !== null,
    )
    .map((r) => r.value)
}
