import React from 'react'

interface SessionSummaryData {
  total_sessions: number
  active_sessions: number
  total_messages: number
  total_tokens: number
  average_session_duration: number
  most_used_tools: Array<{ name: string; count: number }>
}

interface SessionSummaryCardProps {
  data: SessionSummaryData
  title?: string
}

export function SessionSummaryCard({
  data,
  title = "Session Summary"
}: SessionSummaryCardProps) {
  const formatDuration = (minutes: number): string => {
    if (minutes < 60) {
      return `${Math.round(minutes)}m`
    }
    const hours = Math.floor(minutes / 60)
    const remainingMinutes = Math.round(minutes % 60)
    return `${hours}h ${remainingMinutes}m`
  }

  const formatTokens = (tokens: number): string => {
    if (tokens > 1000000) {
      return `${(tokens / 1000000).toFixed(1)}M`
    }
    if (tokens > 1000) {
      return `${(tokens / 1000).toFixed(1)}k`
    }
    return tokens.toString()
  }

  return (
    <div className="bg-gradient-to-br from-blue-50 to-indigo-100 border border-blue-200 rounded-lg p-6">
      <h3 className="text-xl font-bold text-gray-900 mb-4">{title}</h3>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
        <div className="text-center">
          <div className="text-3xl font-bold text-blue-600">
            {data.total_sessions}
          </div>
          <div className="text-sm text-gray-600">Total Sessions</div>
        </div>

        <div className="text-center">
          <div className="text-3xl font-bold text-green-600">
            {data.active_sessions}
          </div>
          <div className="text-sm text-gray-600">Active Now</div>
        </div>

        <div className="text-center">
          <div className="text-3xl font-bold text-purple-600">
            {data.total_messages}
          </div>
          <div className="text-sm text-gray-600">Messages</div>
        </div>

        <div className="text-center">
          <div className="text-3xl font-bold text-orange-600">
            {formatTokens(data.total_tokens)}
          </div>
          <div className="text-sm text-gray-600">Tokens</div>
        </div>
      </div>

      <div className="bg-white rounded-lg p-4 mb-4">
        <div className="flex justify-between items-center">
          <span className="text-gray-700">Average Session Duration:</span>
          <span className="font-semibold text-gray-900">
            {formatDuration(data.average_session_duration)}
          </span>
        </div>
      </div>

      {data.most_used_tools && data.most_used_tools.length > 0 && (
        <div className="bg-white rounded-lg p-4">
          <h4 className="font-semibold text-gray-900 mb-3">Most Used Tools</h4>
          <div className="space-y-2">
            {data.most_used_tools.slice(0, 5).map((tool, index) => (
              <div key={tool.name} className="flex items-center justify-between">
                <div className="flex items-center space-x-2">
                  <div className={`w-2 h-2 rounded-full ${
                    index === 0 ? 'bg-yellow-400' :
                    index === 1 ? 'bg-gray-400' :
                    index === 2 ? 'bg-orange-400' :
                    'bg-blue-400'
                  }`}></div>
                  <span className="text-sm text-gray-700">{tool.name}</span>
                </div>
                <span className="text-sm font-medium text-gray-900">
                  {tool.count}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="mt-4 pt-4 border-t border-blue-200">
        <div className="text-xs text-gray-500 text-center">
          Data refreshed every 5 minutes • Last update: {new Date().toLocaleTimeString()}
        </div>
      </div>
    </div>
  )
}