import React, { useState, useEffect } from 'react'

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
  created_at: string
  updated_at: string
}

interface SessionStatsCardProps {
  sessionId: string
}

export function SessionStatsCard({ sessionId }: SessionStatsCardProps) {
  const [stats, setStats] = useState<SessionStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchStats()
  }, [sessionId])

  const fetchStats = async () => {
    try {
      setLoading(true)
      const response = await fetch(`/api/plugins/session-stats/stats/${sessionId}`)
      if (!response.ok) {
        throw new Error('Failed to fetch session stats')
      }
      const data = await response.json()
      setStats(data)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }

  const formatDuration = (start: string | null, end: string | null): string => {
    if (!start) return 'Not started'

    const startTime = new Date(start)
    const endTime = end ? new Date(end) : new Date()
    const durationMs = endTime.getTime() - startTime.getTime()

    const minutes = Math.floor(durationMs / 60000)
    const seconds = Math.floor((durationMs % 60000) / 1000)

    return `${minutes}m ${seconds}s`
  }

  const formatTokens = (tokens: number): string => {
    if (tokens > 1000) {
      return `${(tokens / 1000).toFixed(1)}k`
    }
    return tokens.toString()
  }

  if (loading) {
    return (
      <div className="bg-white border border-gray-200 rounded-lg p-4">
        <div className="animate-pulse">
          <div className="h-4 bg-gray-300 rounded w-3/4 mb-2"></div>
          <div className="space-y-2">
            {[...Array(4)].map((_, i) => (
              <div key={i} className="h-3 bg-gray-200 rounded w-full"></div>
            ))}
          </div>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="bg-red-50 border border-red-200 rounded-lg p-4">
        <div className="text-red-800 font-medium">Error loading session stats</div>
        <div className="text-red-600 text-sm mt-1">{error}</div>
        <button
          onClick={fetchStats}
          className="mt-2 px-3 py-1 text-sm bg-red-100 hover:bg-red-200 rounded"
        >
          Retry
        </button>
      </div>
    )
  }

  if (!stats) {
    return (
      <div className="bg-gray-50 border border-gray-200 rounded-lg p-4">
        <div className="text-gray-500">No session stats available</div>
      </div>
    )
  }

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-4 space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-gray-900">Session Statistics</h3>
        <button
          onClick={fetchStats}
          className="text-sm text-blue-600 hover:text-blue-800"
        >
          Refresh
        </button>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div className="bg-blue-50 rounded-lg p-3">
          <div className="text-2xl font-bold text-blue-600">
            {stats.message_count}
          </div>
          <div className="text-sm text-blue-600">Messages</div>
        </div>

        <div className="bg-green-50 rounded-lg p-3">
          <div className="text-2xl font-bold text-green-600">
            {formatDuration(stats.start_time, stats.end_time)}
          </div>
          <div className="text-sm text-green-600">Duration</div>
        </div>

        <div className="bg-purple-50 rounded-lg p-3">
          <div className="text-2xl font-bold text-purple-600">
            {formatTokens(stats.total_input_tokens + stats.total_output_tokens)}
          </div>
          <div className="text-sm text-purple-600">Total Tokens</div>
        </div>

        <div className="bg-orange-50 rounded-lg p-3">
          <div className="text-2xl font-bold text-orange-600">
            {stats.tool_calls}
          </div>
          <div className="text-sm text-orange-600">Tool Calls</div>
        </div>
      </div>

      <div className="border-t pt-4 space-y-2 text-sm text-gray-600">
        <div className="flex justify-between">
          <span>Input tokens:</span>
          <span className="font-medium">{formatTokens(stats.total_input_tokens)}</span>
        </div>
        <div className="flex justify-between">
          <span>Output tokens:</span>
          <span className="font-medium">{formatTokens(stats.total_output_tokens)}</span>
        </div>
        <div className="flex justify-between">
          <span>Agent switches:</span>
          <span className="font-medium">{stats.agent_switches}</span>
        </div>
      </div>

      <div className="border-t pt-4">
        <div className="flex space-x-2">
          <button
            onClick={() => window.open(`/api/plugins/session-stats/export/${sessionId}`, '_blank')}
            className="px-3 py-1 text-sm bg-gray-100 hover:bg-gray-200 rounded"
          >
            Export CSV
          </button>
          <button
            onClick={() => {
              navigator.clipboard.writeText(JSON.stringify(stats, null, 2))
              alert('Stats copied to clipboard!')
            }}
            className="px-3 py-1 text-sm bg-blue-100 hover:bg-blue-200 rounded"
          >
            Copy JSON
          </button>
        </div>
      </div>
    </div>
  )
}