import { Component, type ReactNode } from 'react'

interface Props { children: ReactNode; resetKey: unknown; label: string }
interface State { failed: boolean; resetKey: unknown }

/** Reset errors when this export changes, preserving healthy mounted views. */
export class PluginRenderBoundary extends Component<Props, State> {
  state: State = { failed: false, resetKey: undefined }
  static getDerivedStateFromProps(props: Props, state: State) {
    return props.resetKey !== state.resetKey ? { failed: false, resetKey: props.resetKey } : null
  }
  static getDerivedStateFromError() { return { failed: true } }
  render() {
    return this.state.failed
      ? <p role="alert" className="p-3 text-xs text-danger">{this.props.label} could not render.</p>
      : this.props.children
  }
}
