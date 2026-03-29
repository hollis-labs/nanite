import { ChevronDown, ChevronRight, Plus, Server, Terminal, Trash2 } from "lucide-react";
import { useCallback, useState } from "react";
import { Button } from "@/components/ui/button";

interface McpServer {
  name?: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  [key: string]: unknown;
}

interface McpServerListProps {
  /** JSON string of McpServer[] */
  value: string;
  /** Called with new JSON string on change */
  onChange: (json: string) => void;
}

export function McpServerList({ value, onChange }: McpServerListProps) {
  const [expandedIndex, setExpandedIndex] = useState<number | null>(null);
  const [isAdding, setIsAdding] = useState(false);

  const servers: McpServer[] = (() => {
    try {
      const parsed = JSON.parse(value || "[]");
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      return [];
    }
  })();

  const emit = useCallback(
    (updated: McpServer[]) => onChange(JSON.stringify(updated)),
    [onChange],
  );

  const handleRemove = useCallback(
    (index: number) => {
      emit(servers.filter((_, i) => i !== index));
      if (expandedIndex === index) setExpandedIndex(null);
    },
    [servers, emit, expandedIndex],
  );

  const handleAdd = useCallback(
    (server: McpServer) => {
      emit([...servers, server]);
      setIsAdding(false);
      setExpandedIndex(servers.length);
    },
    [servers, emit],
  );

  const handleUpdate = useCallback(
    (index: number, field: string, val: string) => {
      const updated = [...servers];
      updated[index] = { ...updated[index], [field]: val };
      emit(updated);
    },
    [servers, emit],
  );

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-fg-secondary">MCP Servers</span>
        {!isAdding && (
          <Button
            variant="ghost"
            size="sm"
            className="h-6 gap-1 text-[11px] text-fg-muted hover:text-fg"
            onClick={() => setIsAdding(true)}
          >
            <Plus className="w-3 h-3" />
            Add Server
          </Button>
        )}
      </div>

      {servers.length === 0 && !isAdding && (
        <p className="text-xs text-fg-muted py-2">No MCP servers configured</p>
      )}

      <div className="space-y-1">
        {servers.map((srv, index) => {
          const isExpanded = expandedIndex === index;
          const displayName = srv.name || srv.command || `Server ${index + 1}`;
          return (
            <div key={index} className="rounded-lg border border-border-subtle overflow-hidden">
              {/* Server row */}
              <div
                className="flex items-center gap-2 px-3 py-2 cursor-pointer hover:bg-surface/40 transition-colors"
                onClick={() => setExpandedIndex(isExpanded ? null : index)}
              >
                {isExpanded ? (
                  <ChevronDown className="w-3 h-3 text-fg-faint shrink-0" />
                ) : (
                  <ChevronRight className="w-3 h-3 text-fg-faint shrink-0" />
                )}
                <Server className="w-3.5 h-3.5 text-fg-muted shrink-0" />
                <span className="text-xs font-medium text-fg truncate flex-1">
                  {displayName}
                </span>
                {srv.command && !isExpanded && (
                  <span className="text-[10px] font-mono text-fg-faint truncate max-w-[40%]">
                    {srv.command}
                  </span>
                )}
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    handleRemove(index);
                  }}
                  className="p-1 rounded text-fg-faint hover:text-accent transition-colors"
                >
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>

              {/* Expanded detail */}
              {isExpanded && (
                <div className="border-t border-border-subtle bg-bg-elevated/30 px-3 py-2.5 space-y-2.5">
                  <EditRow
                    label="Name"
                    value={srv.name || ""}
                    onChange={(v) => handleUpdate(index, "name", v)}
                    placeholder="server-name"
                  />
                  <EditRow
                    label="Command"
                    value={srv.command || ""}
                    onChange={(v) => handleUpdate(index, "command", v)}
                    placeholder="npx @modelcontextprotocol/server"
                    mono
                    icon={Terminal}
                  />
                  {srv.args && srv.args.length > 0 && (
                    <div className="space-y-1">
                      <span className="text-[10px] text-fg-muted uppercase tracking-wide">Args</span>
                      <div className="flex flex-wrap gap-1">
                        {srv.args.map((arg, i) => (
                          <span key={i} className="text-[11px] font-mono text-fg-secondary bg-surface/60 rounded px-1.5 py-0.5">
                            {arg}
                          </span>
                        ))}
                      </div>
                    </div>
                  )}
                  {srv.env && Object.keys(srv.env).length > 0 && (
                    <div className="space-y-1">
                      <span className="text-[10px] text-fg-muted uppercase tracking-wide">Environment</span>
                      <div className="space-y-0.5">
                        {Object.entries(srv.env).map(([k, v]) => (
                          <div key={k} className="flex items-center gap-2 text-[11px]">
                            <span className="font-mono text-fg-secondary">{k}</span>
                            <span className="text-fg-faint">=</span>
                            <span className="font-mono text-fg-muted truncate">{String(v)}</span>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* Add server inline form */}
      {isAdding && <AddServerForm onAdd={handleAdd} onCancel={() => setIsAdding(false)} />}
    </div>
  );
}

function EditRow({
  label,
  value,
  onChange,
  placeholder,
  mono,
  icon: Icon,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
  icon?: typeof Terminal;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-[10px] text-fg-muted uppercase tracking-wide w-16 shrink-0">{label}</span>
      <div className="flex items-center gap-1.5 flex-1 min-w-0">
        {Icon && <Icon className="w-3 h-3 text-fg-faint shrink-0" />}
        <input
          type="text"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className={`flex-1 min-w-0 bg-transparent text-xs text-fg outline-none border-b border-border-subtle focus:border-accent py-0.5 placeholder:text-fg-faint ${mono ? "font-mono" : ""}`}
        />
      </div>
    </div>
  );
}

function AddServerForm({
  onAdd,
  onCancel,
}: {
  onAdd: (server: McpServer) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [command, setCommand] = useState("");

  const handleSubmit = () => {
    if (!name.trim() && !command.trim()) return;
    onAdd({
      name: name.trim() || undefined,
      command: command.trim() || undefined,
    });
  };

  return (
    <div className="rounded-lg border border-border-subtle bg-bg-elevated/30 px-3 py-2.5 space-y-2.5">
      <span className="text-[11px] font-medium text-fg-secondary">New Server</span>
      <EditRow
        label="Name"
        value={name}
        onChange={setName}
        placeholder="my-server"
      />
      <EditRow
        label="Command"
        value={command}
        onChange={setCommand}
        placeholder="npx @modelcontextprotocol/server"
        mono
        icon={Terminal}
      />
      <div className="flex items-center gap-2 pt-1">
        <Button
          size="sm"
          className="h-6 text-[11px]"
          onClick={handleSubmit}
          disabled={!name.trim() && !command.trim()}
        >
          Add Server
        </Button>
        <Button variant="ghost" size="sm" className="h-6 text-[11px]" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
