import { Globe, Terminal } from "lucide-react";
import { isCLIProviderAlias } from "@/lib/sidebar-session";

const CLI_CONFIG = {
  label: "CLI",
  icon: Terminal,
  bg: "bg-violet-500/10",
  text: "text-violet-400",
  border: "border-violet-500/20",
};
const API_CONFIG = {
  label: "API",
  icon: Globe,
  bg: "bg-success/10",
  text: "text-success",
  border: "border-success/20",
};

function getConfig(provider: string) {
  if (isCLIProviderAlias(provider)) return CLI_CONFIG;
  return API_CONFIG;
}

interface AdapterBadgeProps {
  provider: string;
  size?: "sm" | "md";
}

export function AdapterBadge({ provider, size = "sm" }: AdapterBadgeProps) {
  if (!provider) return null;

  const config = getConfig(provider);
  const Icon = config.icon;
  const isCLI = isCLIProviderAlias(provider);

  if (size === "sm") {
    return (
      <span
        className={`inline-flex items-center gap-0.5 px-1 py-0 rounded text-[10px] font-medium ${config.bg} ${config.text} border ${config.border} leading-relaxed`}
        title={isCLI ? "CLI session" : "API session"}
      >
        <Icon className="w-2.5 h-2.5" />
        {config.label}
      </span>
    );
  }

  return (
    <span
      className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs font-medium ${config.bg} ${config.text} border ${config.border}`}
      title={isCLI ? "CLI session" : "API session"}
    >
      <Icon className="w-3 h-3" />
      {config.label}
    </span>
  );
}
