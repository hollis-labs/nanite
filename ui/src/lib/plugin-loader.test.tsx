import { cleanup, render, waitFor } from '@testing-library/react'
import { Suspense, type ComponentType } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PluginRegistryResponse } from '@hollis-labs/plugin-registry'
import { createReactPluginRegistry } from '@hollis-labs/plugin-registry/react'
import { ENVELOPE_REGISTRY } from '@/generated/plugin-envelopes'
import { createNanitePluginRegistry } from './plugin-loader'

function response(exportName = 'First'): PluginRegistryResponse {
  return {
    protocol: 1,
    plugins: { 'example.plugin': { bundle_url: '/bundle.js', bundle_version: 'accepted' } },
    contributions: { envelope: { 'example.card': { plugin_id: 'example.plugin', export: exportName, meta: { version: 1 } } } },
  }
}

afterEach(cleanup)

describe('Nanite browser registry adoption', () => {
  it('renders a real resolved React export and reconciles manifest changes without reimporting', async () => {
    const importer = vi.fn(async () => ({ First: () => <p>First card</p>, Second: () => <p>Second card</p> }))
    const registry = createNanitePluginRegistry({ importModule: importer, stylesheets: false })
    const initial = await registry.sync(response())
    expect(initial.accepted).toBe(true)
    const First = registry.get('envelope', 'example.card')!.value as ComponentType
    const view = render(<Suspense fallback={<p>Loading</p>}><First /></Suspense>)
    await waitFor(() => expect(view.container.textContent).toBe('First card'))
    await registry.sync(response('Second'))
    const Second = registry.get('envelope', 'example.card')!.value as ComponentType
    view.rerender(<Suspense fallback={<p>Loading</p>}><Second /></Suspense>)
    await waitFor(() => expect(view.container.textContent).toBe('Second card'))
    expect(importer).toHaveBeenCalledTimes(1)
    expect(registry.get('envelope', 'example.card')?.exportName).toBe('Second')
    await registry.sync({ protocol: 1, plugins: {}, contributions: {} })
    expect(registry.get('envelope', 'example.card')).toBeUndefined()
    expect(registry.ownerOf('envelope', 'example.card')).toBeUndefined()
  })

  it('refuses core envelope and widget claims while retaining valid contributions', async () => {
    const registry = createNanitePluginRegistry({ importModule: async () => ({ First: () => null }), stylesheets: false })
    const data = response()
    const coreEnvelope = Object.keys(ENVELOPE_REGISTRY).find((key) => ENVELOPE_REGISTRY[key]?.source === 'core')!
    data.contributions.envelope![coreEnvelope] = { plugin_id: 'example.plugin', export: 'First' }
    data.contributions.widget = { 'session-info': { plugin_id: 'example.plugin', export: 'First' } }
    await registry.sync(data)
    expect(registry.get('envelope', coreEnvelope)).toBeUndefined()
    expect(registry.refusals()).toContainEqual(expect.objectContaining({ kind: 'envelope', key: coreEnvelope, reason: 'reserved' }))
    expect(registry.get('widget', 'session-info')).toBeUndefined()
    expect(registry.ownerOf('widget', 'session-info')).toBeUndefined()
    expect(registry.refusals()).toContainEqual(expect.objectContaining({ kind: 'widget', key: 'session-info', reason: 'reserved' }))
    expect(registry.get('envelope', 'example.card')).toBeDefined()
  })

  it('preserves the last valid registry on protocol refusal and isolates failed plugins', async () => {
    const registry = createNanitePluginRegistry({ importModule: async (url) => {
      if (url.startsWith('/bad.js')) throw new Error('bundle unavailable')
      return { First: () => null }
    }, stylesheets: false })
    const data = response()
    data.plugins.broken = { bundle_url: '/bad.js' }
    data.contributions.envelope!['broken.card'] = { plugin_id: 'broken', export: 'Missing' }
    await registry.sync(data)
    expect(registry.get('envelope', 'example.card')).toBeDefined()
    expect(registry.ownerOf('envelope', 'broken.card')).toBe('broken')
    expect(registry.errors()).toContainEqual(expect.objectContaining({ pluginId: 'broken', reason: 'bundle unavailable' }))
    const refused = await registry.sync({ ...data, protocol: 2 })
    expect(refused.accepted).toBe(false)
    expect(registry.get('envelope', 'example.card')).toBeDefined()
  })
})

it.skipIf(!import.meta.env['VITE_NANITE_PLUGIN_SMOKE_URL'])('renders a bundle served by the real Nanite registry fixture', async () => {
  const base = String(import.meta.env['VITE_NANITE_PLUGIN_SMOKE_URL'])
  const dom = window as unknown as { happyDOM: { setURL(url: string): void } }
  const previousURL = window.location.href
  dom.happyDOM.setURL(base)
  try {
    const httpResponse = await fetch(`${base}/api/plugins/registry`)
    expect(httpResponse.ok).toBe(true)
    const data = await httpResponse.json() as PluginRegistryResponse
    // Fetch the real served bundle, then let the released SDK's native ESM
    // importer load its bytes. Node cannot import an HTTP URL directly.
    const plugin = data.plugins['browser-live']!
    const bundle = await fetch(new URL(plugin.bundle_url!, base))
    expect(bundle.ok).toBe(true)
    expect(plugin.bundle_version).toMatch(/^[a-f0-9]{64}$/)
    plugin.bundle_url = `data:text/javascript;base64,${btoa(await bundle.text())}`
    // A data URI already identifies its bytes and cannot take the SDK's
    // HTTP cache-version query parameter.
    delete plugin.bundle_version
    const registry = createReactPluginRegistry({ stylesheets: false })
    const result = await registry.sync(data)
    expect(result.accepted).toBe(true)
    expect(registry.errors()).toEqual([])
    const contribution = registry.get('envelope', 'browser-live-card')!
    expect(contribution.pluginId).toBe('browser-live')
    const LiveCard = contribution.value as ComponentType
    const view = render(<Suspense fallback={null}><LiveCard /></Suspense>)
    await waitFor(() => expect(view.container.textContent).toBe('Loaded from Nanite'))
    registry.clear()
  } finally {
    try {
      await fetch(`${base}/smoke/stop`, { method: 'POST' })
    } finally {
      dom.happyDOM.setURL(previousURL)
    }
  }
})
