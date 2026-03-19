import { useState, useEffect } from 'react'

interface SessionStats {
  id: string
  session_id: string
  message_count: number
  start_time: string | null
  end_time: string | null
  total_input_tokens: number
  total_output_tokens: number
  agent_switches: number
  tool_calls: number
}

interface SessionStatsWidgetProps {
  sessionId: string
  compact?: boolean
}

export function SessionStatsWidget({ sessionId, compact = true }: SessionStatsWidgetProps) {
  const [stats, setStats] = useState<SessionStats | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetchStats()
    const interval = setInterval(fetchStats, 30000) // Refresh every 30 seconds
    return () => clearInterval(interval)
  }, [sessionId])

  const fetchStats = async () => {
    try {
      const response = await fetch(`/api/plugins/session-stats/stats/${sessionId}`)
      if (response.ok) {
        const data = await response.json()
        setStats(data)
      }
    } catch (error) {
      console.error('Failed to fetch session stats:', error)
    } finally {
      setLoading(false)
    }
  }

  const formatTokens = (tokens: number): string => {
    if (tokens > 1000) {
      return `${(tokens / 1000).toFixed(1)}k`
    }
    return tokens.toString()
  }

  const formatDuration = (start: string | null, end: string | null): string => {
    if (!start) return '0m'

    const startTime = new Date(start)
    const endTime = end ? new Date(end) : new Date()
    const durationMs = endTime.getTime() - startTime.getTime()
    const minutes = Math.floor(durationMs / 60000)

    if (minutes < 60) {
      return `${minutes}m`
    }
    return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
  }

  if (loading) {
    return (
      <div className="bg-white border border-gray-200 rounded-lg p-3">
        <div className="animate-pulse">
          <div className="h-4 bg-gray-300 rounded w-20 mb-2"></div>
          <div className="space-y-1">
            {[...Array(3)].map((_, i) => (
              <div key={i} className="h-3 bg-gray-200 rounded w-full"></div>
            ))}
          </div>
        </div>
      </div>
    )
  }

  if (!stats) {
    return (
      <div className="bg-gray-50 border border-gray-200 rounded-lg p-3">
        <div className="text-xs text-gray-500">Stats unavailable</div>
      </div>
    )
  }

  if (compact) {
    return (
      <div className="bg-white border border-gray-200 rounded-lg p-3 space-y-2">
        <div className="flex items-center justify-between">
          <h4 className="text-sm font-medium text-gray-900">Session Stats</h4>
          <button
            onClick={fetchStats}
            className="text-xs text-gray-500 hover:text-gray-700"
            title="Refresh stats"
          >
            ↻
          </button>
        </div>

        <div className="space-y-1 text-xs">
          <div className="flex justify-between">
            <span className="text-gray-600">Messages:</span>
            <span className="font-medium">{stats.message_count}</span>
          </div>

          <div className="flex justify-between">
            <span className="text-gray-600">Tokens:</span>
            <span className="font-medium">
              {formatTokens(stats.total_input_tokens + stats.total_output_tokens)}
            </span>
          </div>

          <div className="flex justify-between">
            <span className="text-gray-600">Duration:</span>
            <span className="font-medium">
              {formatDuration(stats.start_time, stats.end_time)}
            </span>
          </div>

          <div className="flex justify-between">
            <span className="text-gray-600">Tools:</span>
            <span className="font-medium">{stats.tool_calls}</span>
          </div>
        </div>

        {stats.end_time && (
          <div className="pt-1 border-t border-gray-100">
            <div className="text-xs text-green-600 font-medium">Session completed</div>
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="font-medium text-gray-900">Session Statistics</h4>
        <button
          onClick={fetchStats}
          className="text-sm text-gray-500 hover:text-gray-700"
        >
          ↻ Refresh
        </button>
      </div>

      <div className="grid grid-cols-2 gap-2 text-sm">
        <div className="bg-blue-50 rounded p-2 text-center">
          <div className="font-bold text-blue-600">{stats.message_count}</div>
          <div className="text-blue-600 text-xs">Messages</div>
        </div>

        <div className="bg-green-50 rounded p-2 text-center">
          <div className="font-bold text-green-600">
            {formatDuration(stats.start_time, stats.end_time)}
          </div>
          <div className="text-green-600 text-xs">Duration</div>
        </div>

        <div className="bg-purple-50 rounded p-2 text-center">
          <div className="font-bold text-purple-600">
            {formatTokens(stats.total_input_tokens + stats.total_output_tokens)}
          </div>
          <div className="text-purple-600 text-xs">Tokens</div>
        </div>

        <div className="bg-orange-50 rounded p-2 text-center">
          <div className="font-bold text-orange-600">{stats.tool_calls}</div>
          <div className="text-orange-600 text-xs">Tools</div>
        </div>
      </div>

      {stats.agent_switches > 0 && (
        <div className="text-xs text-gray-600">
          Agent switches: <span className="font-medium">{stats.agent_switches}</span>
        </div>
      )}
    </div>
  )
}