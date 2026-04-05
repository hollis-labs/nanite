import { Clock, Repeat, RotateCcw } from "lucide-react";
import { useCallback } from "react";

interface Constraints {
  max_iterations?: number;
  max_time_seconds?: number;
  retry_budget?: number;
}

interface ConstraintsEditorProps {
  /** JSON string of constraints object */
  value: string;
  /** Called with new JSON string on change */
  onChange: (json: string) => void;
}

const FIELDS: ReadonlyArray<{
  key: keyof Constraints;
  label: string;
  icon: typeof Repeat;
  placeholder: string;
  hint: string;
  suffix?: string;
}> = [
  {
    key: "max_iterations",
    label: "Max Iterations",
    icon: Repeat,
    placeholder: "10",
    hint: "Maximum tool call loops",
  },
  {
    key: "max_time_seconds",
    label: "Max Time",
    icon: Clock,
    placeholder: "300",
    hint: "Seconds before timeout",
    suffix: "s",
  },
  {
    key: "retry_budget",
    label: "Retry Budget",
    icon: RotateCcw,
    placeholder: "3",
    hint: "Automatic retries on failure",
  },
];

export function ConstraintsEditor({ value, onChange }: ConstraintsEditorProps) {
  const constraints: Constraints = (() => {
    try {
      const parsed = JSON.parse(value || "{}");
      return typeof parsed === "object" && parsed !== null ? parsed : {};
    } catch {
      return {};
    }
  })();

  const handleChange = useCallback(
    (key: keyof Constraints, raw: string) => {
      const updated = { ...constraints };
      if (raw === "" || raw === undefined) {
        delete updated[key];
      } else {
        const num = Number(raw);
        if (!Number.isNaN(num) && num >= 0) {
          updated[key] = num;
        }
      }
      // Only emit keys with values
      const clean = Object.fromEntries(
        Object.entries(updated).filter(([, v]) => v !== null && v !== undefined),
      );
      onChange(JSON.stringify(clean));
    },
    [constraints, onChange],
  );

  return (
    <div className="space-y-2">
      <span className="text-xs font-medium text-fg-secondary">Constraints</span>
      <div className="grid grid-cols-3 gap-3">
        {FIELDS.map(({ key, label, icon: Icon, placeholder, hint, suffix }) => (
          <div key={key} className="space-y-1">
            <div className="flex items-center gap-1.5">
              <Icon className="w-3 h-3 text-fg-muted" />
              <span className="text-[11px] text-fg-muted">{label}</span>
            </div>
            <div className="relative">
              <input
                type="number"
                min={0}
                value={constraints[key] ?? ""}
                onChange={(e) => handleChange(key, e.target.value)}
                placeholder={placeholder}
                className="w-full px-2 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg font-mono focus:outline-none focus:border-primary tabular-nums placeholder:text-fg-faint"
              />
              {suffix && (
                <span className="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] text-fg-faint pointer-events-none">
                  {suffix}
                </span>
              )}
            </div>
            <p className="text-[10px] text-fg-faint leading-tight">{hint}</p>
          </div>
        ))}
      </div>
    </div>
  );
}
