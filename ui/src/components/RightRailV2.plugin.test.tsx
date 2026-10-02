import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { browserPluginRegistry } from '@/lib/plugin-loader'
import { usePanelRegistryStore } from '@/hooks/usePanelRegistry'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { RightRailV2 } from './RightRailV2'

vi.mock('@/hooks/useSettings', () => ({
  useSettings: () => ({ data: { developer_mode: false } }), useSettingsMutation: () => ({ mutate: vi.fn() }),
}))
vi.mock('./widgets/WidgetRenderer', () => ({ WidgetRenderer: () => null }))
vi.mock('./work/WorkTab', () => ({ WorkTab: () => null }))
vi.mock('./workflows/WorkflowTab', () => ({ WorkflowTab: () => null }))
vi.mock('./messaging/InboxContent', () => ({ InboxContent: () => null }))
vi.mock('./drawers/ArtifactsContent', () => ({ ArtifactsContent: () => null }))

afterEach(() => {
  cleanup(); browserPluginRegistry.clear()
  usePanelRegistryStore.setState({ panels: {}, orderedIds: [] })
})

it('renders a manifest panel in the real rail and falls back to core after unload', async () => {
  await browserPluginRegistry.sync({ protocol: 1,
    plugins: { docs: { bundle_url: 'data:text/javascript,export function Docs(props) { return "Plugin documents:" + props.session_id }' } },
    contributions: { panel: { documents: { plugin_id: 'docs', export: 'Docs', meta: { title: 'Plugin documents', default_visible: true, order: 110 } } } },
  })
  useAppStore.setState({ activeSessionId: 'session-a' })
  useLayoutStore.setState({ rightRailOpen: true, rightRailTab: 'documents' })
  const client = new QueryClient()
  client.setQueryData(['plugin-ui-components'], [])
  const view = render(<QueryClientProvider client={client}><RightRailV2 /></QueryClientProvider>)
  await waitFor(() => expect(view.container.textContent).toContain('Plugin documents:session-a'))
  expect(view.getByRole('button', { name: 'Plugin documents' })).toBeDefined()
  expect(view.container.textContent).not.toContain('Render function pending')
  act(() => useLayoutStore.setState((state) => ({ panelPrefs: { ...state.panelPrefs,
    panelEnabled: { ...state.panelPrefs.panelEnabled, documents: false } } })))
  expect(view.queryByRole('button', { name: 'Plugin documents' })).toBeNull()
  await act(async () => { await browserPluginRegistry.sync({ protocol: 1, plugins: {}, contributions: {} }) })
  expect(view.queryByRole('button', { name: 'Plugin documents' })).toBeNull()
  expect(view.container.textContent).not.toContain('Plugin documents:session-a')
  expect(usePanelRegistryStore.getState().panels.widgets.source).toBe('builtin')
  client.clear()
})
