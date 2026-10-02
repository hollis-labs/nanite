import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { PluginRegistryResponse } from '@hollis-labs/plugin-registry'
import { browserPluginRegistry } from '@/lib/plugin-loader'
import { usePluginPanels } from '@/hooks/usePluginPanels'
import { usePanelRegistryStore } from '@/hooks/usePanelRegistry'
import { PluginPanelBody } from './PluginPanelBody'
import { PluginRenderBoundary } from './PluginRenderBoundary'

afterEach(() => {
  cleanup(); browserPluginRegistry.clear()
  usePanelRegistryStore.setState({ panels: {}, orderedIds: [] })
  vi.restoreAllMocks()
})

function Harness() {
  const panels = usePluginPanels()
  return <>{panels.map((panel) => <PluginPanelBody key={panel.definition.id} panel={panel} sessionId="session-current" />)}</>
}

function declaration(exportName = 'First'): PluginRegistryResponse {
  return {
    protocol: 1,
    plugins: { 'docs.plugin': { bundle_url: 'data:text/javascript,export function First(props) { return "First:" + props.session_id }; export function Second(props) { return "Second:" + props.session_id }' } },
    contributions: { panel: { docs: { plugin_id: 'docs.plugin', export: exportName,
      meta: { title: 'Documents', icon: 'file-text', default_visible: true, order: 130 } } } },
  }
}

it('renders manifest panels, reconciles export changes and removes owner metadata on unload', async () => {
  await browserPluginRegistry.sync(declaration())
  const view = render(<Harness />)
  await waitFor(() => expect(view.container.textContent).toBe('First:session-current'))
  expect(usePanelRegistryStore.getState().panels.docs).toMatchObject({ label: 'Documents', pluginId: 'docs.plugin', defaultVisible: true, order: 130 })
  await act(async () => { await browserPluginRegistry.sync(declaration('Second')) })
  await waitFor(() => expect(view.container.textContent).toBe('Second:session-current'))
  await act(async () => { await browserPluginRegistry.sync({ protocol: 1, plugins: {}, contributions: {} }) })
  expect(view.container.textContent).toBe('')
  expect(usePanelRegistryStore.getState().panels.docs).toBeUndefined()
})

it('refuses core panel and rail slot claims while preserving core registry definitions', async () => {
  const core = { id: 'work', label: 'Plan', source: 'builtin' as const }
  usePanelRegistryStore.getState().register(core)
  const data = declaration()
  data.contributions.panel!.work = { plugin_id: 'docs.plugin', export: 'First', meta: { title: 'Claim' } }
  data.contributions.slot = { 'right-rail-tab/work': { plugin_id: 'docs.plugin', export: 'Second', meta: { id: 'work', label: 'Claim' } } }
  await browserPluginRegistry.sync(data)
  render(<Harness />)
  expect(browserPluginRegistry.refusals()).toEqual(expect.arrayContaining([
    expect.objectContaining({ kind: 'panel', key: 'work', reason: 'reserved' }),
    expect.objectContaining({ kind: 'slot', key: 'right-rail-tab/work', reason: 'reserved' }),
  ]))
  usePanelRegistryStore.getState().register({ id: 'work', label: 'Claim', source: 'plugin', pluginId: 'docs.plugin' })
  expect(usePanelRegistryStore.getState().panels.work).toEqual(core)
})

it('preserves an edited form when the boundary reset identity changes', () => {
  const view = render(<PluginRenderBoundary resetKey="first" label="Plugin panel"><input aria-label="Draft" defaultValue="saved" /></PluginRenderBoundary>)
  fireEvent.change(view.getByRole('textbox'), { target: { value: 'unsaved work' } })
  view.rerender(<PluginRenderBoundary resetKey="second" label="Plugin panel"><input aria-label="Draft" defaultValue="saved" /></PluginRenderBoundary>)
  expect((view.getByRole('textbox') as HTMLInputElement).value).toBe('unsaved work')
})
