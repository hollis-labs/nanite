// Session Stats Plugin Event Hooks
// This file demonstrates how plugin event hooks would integrate with Conduit's event system

class SessionStatsHooks {
  constructor(apiBaseUrl, pluginRegistry) {
    this.apiBaseUrl = apiBaseUrl
    this.pluginRegistry = pluginRegistry
  }

  // Register event handlers with Conduit's event system
  register() {
    // Session lifecycle hooks
    this.pluginRegistry.on('session.start', this.onSessionStart.bind(this))
    this.pluginRegistry.on('session.end', this.onSessionEnd.bind(this))
    this.pluginRegistry.on('message.received', this.onMessageReceived.bind(this))
    this.pluginRegistry.on('tool.called', this.onToolCalled.bind(this))
    this.pluginRegistry.on('mode.changed', this.onModeChanged.bind(this))

    console.log('Session Stats plugin hooks registered')
  }

  // Handle session start event
  async onSessionStart(event) {
    const { session_id, agent_id, mode } = event.payload

    try {
      // Create or update session stats
      const response = await fetch(`${this.apiBaseUrl}/plugins/session-stats/stats/${session_id}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          start_time: new Date().toISOString(),
          message_count: 0
        })
      })

      if (!response.ok) {
        console.error('Failed to initialize session stats:', response.statusText)
        return
      }

      console.log(`Session stats initialized for session ${session_id}`)

      // Optionally emit a custom envelope to show session started
      this.emitSessionStatsEnvelope(session_id, 'session_started', {
        message: `Session started with agent ${agent_id}`,
        mode: mode
      })

    } catch (error) {
      console.error('Error handling session start:', error)
    }
  }

  // Handle session end event
  async onSessionEnd(event) {
    const { session_id } = event.payload

    try {
      // Update session end time
      const response = await fetch(`${this.apiBaseUrl}/plugins/session-stats/stats/${session_id}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          end_time: new Date().toISOString()
        })
      })

      if (!response.ok) {
        console.error('Failed to update session end time:', response.statusText)
        return
      }

      console.log(`Session stats finalized for session ${session_id}`)

      // Get final stats and emit summary envelope
      const statsResponse = await fetch(`${this.apiBaseUrl}/plugins/session-stats/stats/${session_id}`)
      if (statsResponse.ok) {
        const stats = await statsResponse.json()
        this.emitSessionStatsEnvelope(session_id, 'session_completed', stats)
      }

    } catch (error) {
      console.error('Error handling session end:', error)
    }
  }

  // Handle message received event
  async onMessageReceived(event) {
    const { session_id, role, content, agent_id } = event.payload

    try {
      // Update message count
      await fetch(`${this.apiBaseUrl}/plugins/session-stats/stats/${session_id}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          message_count: 'INCREMENT' // Special value to increment
        })
      })

      // Track token usage if available
      if (event.payload.usage) {
        const { input_tokens, output_tokens } = event.payload.usage
        await fetch(`${this.apiBaseUrl}/plugins/session-stats/stats/${session_id}`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            total_input_tokens: `ADD:${input_tokens}`,
            total_output_tokens: `ADD:${output_tokens}`
          })
        })
      }

    } catch (error) {
      console.error('Error handling message received:', error)
    }
  }

  // Handle tool call event
  async onToolCalled(event) {
    const { session_id, tool_name, status } = event.payload

    if (status === 'done') {
      try {
        // Increment tool call count
        await fetch(`${this.apiBaseUrl}/plugins/session-stats/stats/${session_id}`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            tool_calls: 'INCREMENT'
          })
        })

      } catch (error) {
        console.error('Error handling tool call:', error)
      }
    }
  }

  // Handle mode change event (for agent switches)
  async onModeChanged(event) {
    const { session_id, old_mode, new_mode } = event.payload

    if (old_mode !== new_mode) {
      try {
        // Increment agent switch count
        await fetch(`${this.apiBaseUrl}/plugins/session-stats/stats/${session_id}`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            agent_switches: 'INCREMENT'
          })
        })

      } catch (error) {
        console.error('Error handling mode change:', error)
      }
    }
  }

  // Emit a session stats envelope for display in chat
  emitSessionStatsEnvelope(sessionId, eventType, data) {
    // This would integrate with Conduit's envelope system
    const envelope = {
      kind: 'conduit-envelope',
      version: 1,
      type: 'session-stats',
      data: {
        event_type: eventType,
        session_id: sessionId,
        data: data,
        timestamp: new Date().toISOString()
      }
    }

    // In a real implementation, this would add the envelope to the message stream
    console.log('Would emit envelope:', envelope)
  }

  // Quick action handler for showing session stats
  async showSessionStats(sessionId) {
    try {
      const response = await fetch(`${this.apiBaseUrl}/plugins/session-stats/stats/${sessionId}`)
      if (response.ok) {
        const stats = await response.json()

        // Emit an envelope with session stats for display
        this.emitSessionStatsEnvelope(sessionId, 'stats_display', stats)
      }
    } catch (error) {
      console.error('Error showing session stats:', error)
    }
  }

  // Quick action handler for exporting session stats
  async exportSessionStats(sessionId) {
    try {
      // Trigger download
      const link = document.createElement('a')
      link.href = `${this.apiBaseUrl}/plugins/session-stats/export/${sessionId}`
      link.download = `session_${sessionId}_stats.csv`
      link.click()
    } catch (error) {
      console.error('Error exporting session stats:', error)
    }
  }
}

// Plugin initialization function
export function initializeSessionStatsPlugin(apiBaseUrl, pluginRegistry) {
  const hooks = new SessionStatsHooks(apiBaseUrl, pluginRegistry)
  hooks.register()

  // Return handlers for quick actions
  return {
    showSessionStats: hooks.showSessionStats.bind(hooks),
    exportSessionStats: hooks.exportSessionStats.bind(hooks)
  }
}

// Default export for plugin loader
export default {
  name: 'session-stats',
  version: '1.0.0',
  initialize: initializeSessionStatsPlugin
}