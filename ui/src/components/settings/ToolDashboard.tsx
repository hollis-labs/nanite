export function ToolDashboard() {
  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-zinc-100 mb-2">Tools & MCP Servers</h2>
        <p className="text-zinc-400 text-sm">
          Manage connected MCP servers and their discovered tools.
        </p>
      </div>

      <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-8">
        <div className="text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-lg bg-zinc-800 flex items-center justify-center">
            <span className="text-2xl text-zinc-500">🔧</span>
          </div>
          <h3 className="text-lg font-medium text-zinc-100 mb-2">Tool Dashboard</h3>
          <p className="text-zinc-400 text-sm">
            This section will display MCP servers, tools, and management controls.
          </p>
        </div>
      </div>
    </div>
  )
}