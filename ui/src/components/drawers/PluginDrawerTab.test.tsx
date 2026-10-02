import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { PluginRegistryResponse } from '@hollis-labs/plugin-registry'
import { browserPluginRegistry } from '@/lib/plugin-loader'
import { api } from '@/lib/api'
import { useAppStore } from '@/stores/useAppStore'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { ChatPrimaryDrawer } from './ChatPrimaryDrawer'
import { ChatWorkingDrawer } from './ChatWorkingDrawer'
import { PluginDrawerTabBody, type PluginDrawerTab } from './PluginDrawerTab'

vi.mock('@/hooks/useSettings', () => ({ useSettings: () => ({ data: { developer_mode: false } }) }))

afterEach(() => { cleanup(); browserPluginRegistry.clear(); vi.restoreAllMocks() })

it('contains a failed plugin render and recovers when its declaration changes', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => {})
  const data: PluginRegistryResponse = {
    protocol: 1, plugins: { broken: { bundle_url: 'data:text/javascript,export function Broken() { throw new Error("fixture") }; export function Fixed() { return "Recovered" }' } },
    contributions: { slot: { 'drawer.primary.tabs/broken': { plugin_id: 'broken', export: 'Broken', meta: { id: 'broken' } } } },
  }
  await browserPluginRegistry.sync(data)
  const entry: PluginDrawerTab = { tabId: 'plugin:broken:broken', id: 'broken', plugin_id: 'broken', component: 'Broken', label: 'Broken', slot: 'drawer.primary.tabs' }
  const view = render(<PluginDrawerTabBody entry={entry} sessionId="session-a" />)
  await waitFor(() => expect(view.getByRole('alert').textContent).toBe('Plugin tab could not render.'))
  data.contributions.slot!['drawer.primary.tabs/broken']!.export = 'Fixed'
  await browserPluginRegistry.sync(data)
  view.rerender(<PluginDrawerTabBody entry={{ ...entry, component: 'Fixed' }} sessionId="session-a" />)
  await waitFor(() => expect(view.container.textContent).toBe('Recovered'))
})

for (const [slot, Drawer] of [
  ['drawer.primary.tabs', ChatPrimaryDrawer], ['drawer.working.tabs', ChatWorkingDrawer],
] as const) {
  it(`${slot} renders the owning export and session, then removes it on unload`, async () => {
    vi.spyOn(api, 'listDrawerCards').mockResolvedValue([])
    const data: PluginRegistryResponse = {
      protocol: 1,
      plugins: { 'docs.plugin': { bundle_url: 'data:text/javascript,export function Shared(props) { return props.session_id + ":" + props.caption }' },
        'other.plugin': { bundle_url: 'data:text/javascript,export function Shared() { return "Wrong owner" }' } },
      contributions: { slot: {
        [`${slot}/docs`]: { plugin_id: 'docs.plugin', export: 'Shared', meta: { id: 'docs', label: 'Plugin documents', icon: 'file-text', props: { caption: 'Owned view', session_id: 'stale' } } },
        'modal/other': { plugin_id: 'other.plugin', export: 'Shared', meta: { id: 'other' } },
      } },
    }
    await browserPluginRegistry.sync(data)
    expect(browserPluginRegistry.errors()).toEqual([])
    const store = useLayoutStore.getState()
    store.setChatPrimaryDrawer({ open: true, activeTab: 'plugin:docs.plugin:docs' })
    store.setChatWorkingDrawer({ open: true, activeTab: 'plugin:docs.plugin:docs' }, 'session-a')
    useAppStore.setState({ activeSessionId: null })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const view = render(<QueryClientProvider client={client}><Drawer /></QueryClientProvider>)
    act(() => useAppStore.setState({ activeSessionId: 'session-a' }))
    await waitFor(() => expect(view.container.textContent).toContain('session-a:Owned view'))
    expect(view.container.textContent).not.toContain('Wrong owner')
    expect(view.getByRole('button', { name: 'Plugin documents' })).toBeDefined()
    // State and hook ordering survive an inactive/active session transition.
    act(() => useAppStore.setState({ activeSessionId: null }))
    act(() => useAppStore.setState({ activeSessionId: 'session-a' }))
    await waitFor(() => expect(view.container.textContent).toContain('session-a:Owned view'))
    await act(async () => { await browserPluginRegistry.sync({ protocol: 1, plugins: {}, contributions: {} }) })
    expect(view.queryByRole('button', { name: 'Plugin documents' })).toBeNull()
    expect(view.container.textContent).toContain('Plugin tab is unavailable.')
    fireEvent.click(view.getByRole('button', { name: slot === 'drawer.primary.tabs' ? 'Tools' : 'Scratchpad' }))
    expect(view.container.textContent).not.toContain('Plugin tab is unavailable.')
    client.clear()
  })
}
