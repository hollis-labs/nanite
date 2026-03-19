import { useState } from 'react'

interface QuickActionButtonProps {
  sessionId: string
  actionId: string
  label: string
  icon: string
  context: string[]
  onAction?: (actionId: string, sessionId: string) => void
}

export function SessionStatsQuickActionButton({
  sessionId,
  actionId,
  label,
  icon,
  context,
  onAction
}: QuickActionButtonProps) {
  const [loading, setLoading] = useState(false)

  const handleClick = async () => {
    setLoading(true)
    try {
      if (onAction) {
        await onAction(actionId, sessionId)
      } else {
        // Default handlers for session-stats plugin actions
        switch (actionId) {
          case 'show-session-stats':
            await showSessionStats(sessionId)
            break
          case 'export-session-stats':
            await exportSessionStats(sessionId)
            break
          default:
            console.warn('Unknown action ID:', actionId)
        }
      }
    } catch (error) {
      console.error('Action failed:', error)
    } finally {
      setLoading(false)
    }
  }

  const getIconComponent = (iconName: string) => {
    switch (iconName) {
      case 'chart-bar':
        return (
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
              d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
          </svg>
        )
      case 'download':
        return (
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
              d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
          </svg>
        )
      default:
        return (
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 5v.01M12 12v.01M12 19v.01M12 6a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2z" />
          </svg>
        )
    }
  }

  return (
    <button
      onClick={handleClick}
      disabled={loading}
      className={`
        inline-flex items-center space-x-1 px-3 py-1.5 text-sm rounded-md
        transition-colors duration-200
        ${loading
          ? 'bg-gray-100 text-gray-400 cursor-not-allowed'
          : 'bg-blue-100 text-blue-700 hover:bg-blue-200 hover:text-blue-800'
        }
      `}
      title={`${label} for session ${sessionId}`}
    >
      {loading ? (
        <div className="w-4 h-4 border-2 border-blue-300 border-t-blue-600 rounded-full animate-spin"></div>
      ) : (
        getIconComponent(icon)
      )}
      <span>{label}</span>
    </button>
  )
}

// Action handlers
async function showSessionStats(sessionId: string) {
  try {
    const response = await fetch(`/api/plugins/session-stats/stats/${sessionId}`)
    if (!response.ok) {
      throw new Error('Failed to fetch session stats')
    }

    const stats = await response.json()

    // Create and emit an envelope with session stats
    const envelope = {
      kind: 'conduit-envelope',
      version: 1,
      type: 'session-stats',
      data: {
        event_type: 'stats_display',
        session_id: sessionId,
        stats: stats,
        timestamp: new Date().toISOString()
      }
    }

    // In a real implementation, this would integrate with Conduit's message system
    // to add the envelope to the current conversation
    console.log('Session stats envelope:', envelope)

    // For demo purposes, show an alert
    alert(`Session Stats for ${sessionId}:
Messages: ${stats.message_count}
Tokens: ${stats.total_input_tokens + stats.total_output_tokens}
Tool Calls: ${stats.tool_calls}`)

  } catch (error) {
    console.error('Failed to show session stats:', error)
    alert('Failed to load session stats. Please try again.')
  }
}

async function exportSessionStats(sessionId: string) {
  try {
    // Trigger CSV download
    const link = document.createElement('a')
    link.href = `/api/plugins/session-stats/export/${sessionId}`
    link.download = `session_${sessionId}_stats.csv`
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)

    // Show success feedback
    const notification = document.createElement('div')
    notification.textContent = 'Session stats exported successfully!'
    notification.className = 'fixed top-4 right-4 bg-green-100 border border-green-400 text-green-700 px-4 py-3 rounded z-50'
    document.body.appendChild(notification)

    // Remove notification after 3 seconds
    setTimeout(() => {
      if (notification.parentNode) {
        document.body.removeChild(notification)
      }
    }, 3000)

  } catch (error) {
    console.error('Failed to export session stats:', error)
    alert('Failed to export session stats. Please try again.')
  }
}

// Container component for multiple quick actions
interface SessionStatsQuickActionsProps {
  sessionId: string
  actions?: Array<{
    id: string
    label: string
    icon: string
    context: string[]
  }>
}

export function SessionStatsQuickActions({
  sessionId,
  actions = [
    { id: 'show-session-stats', label: 'Session Stats', icon: 'chart-bar', context: ['session'] },
    { id: 'export-session-stats', label: 'Export Stats', icon: 'download', context: ['session'] }
  ]
}: SessionStatsQuickActionsProps) {
  return (
    <div className="flex flex-wrap gap-2">
      {actions.map((action) => (
        <SessionStatsQuickActionButton
          key={action.id}
          sessionId={sessionId}
          actionId={action.id}
          label={action.label}
          icon={action.icon}
          context={action.context}
        />
      ))}
    </div>
  )
}