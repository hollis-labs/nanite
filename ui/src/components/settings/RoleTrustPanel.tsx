// RoleTrustPanel — H1 Role Trust settings UI (CW-20260421-0014)
// Lists every agent profile and its effective trust tier in the active
// workspace. Promotes/demotes via the existing API endpoints.
// v1: workspace_id from activeWorkspaceId store (falls back to "dogfood").
// TODO(follow-up): thread real workspace switching context once UI plumbing lands.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, Shield, ShieldAlert, ShieldCheck } from "lucide-react";
import { api } from "@/lib/api";
import type { AgentProfile, TrustTier } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { PanelHeader, SCard } from "./primitives";
import { Skeleton } from "@/components/ui/skeleton";

// --- Tier display helpers ---

const TIER_LABELS: Record<TrustTier, string> = {
  trusted: "Trusted",
  normal: "Normal",
  untrusted: "Untrusted",
};

const TIER_DESCRIPTIONS: Record<TrustTier, string> = {
  trusted: "Dispatches bypass the approval gate. All bypasses are audit-logged.",
  normal: "Dispatches require approval when SubagentApprovalRequired is enabled.",
  untrusted: "Dispatches are refused outright in this workspace.",
};

type TierBadgeVariant = "trusted" | "normal" | "untrusted";

function TierBadge({ tier }: { tier: TierBadgeVariant }) {
  const base = "inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium";
  switch (tier) {
    case "trusted":
      return (
        <span className={`${base} bg-status-ok/15 text-status-ok`}>
          <ShieldCheck style={{ width: 11, height: 11 }} />
          Trusted
        </span>
      );
    case "untrusted":
      return (
        <span className={`${base} bg-status-danger/15 text-status-danger`}>
          <ShieldAlert style={{ width: 11, height: 11 }} />
          Untrusted
        </span>
      );
    default:
      return (
        <span className={`${base} bg-fg-secondary/15 text-fg-secondary`}>
          <Shield style={{ width: 11, height: 11 }} />
          Normal
        </span>
      );
  }
}

// --- Tier action buttons ---

const TIERS: TrustTier[] = ["untrusted", "normal", "trusted"];

function TierSelector({
  agentProfileID,
  currentTier,
  workspaceID,
}: {
  agentProfileID: string;
  currentTier: TrustTier;
  workspaceID: string;
}) {
  const queryClient = useQueryClient();

  const setTierMutation = useMutation({
    mutationFn: async (tier: TrustTier) => {
      if (tier === "normal") {
        // "normal" means: remove the override and fall back to profile default.
        return api.deleteWorkspaceRoleTrust(workspaceID, agentProfileID);
      }
      return api.setWorkspaceRoleTrust(workspaceID, agentProfileID, tier);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["workspace-role-trust", workspaceID],
      });
    },
  });

  return (
    <div className="flex items-center gap-1">
      {TIERS.map((tier) => {
        const isActive = tier === currentTier;
        const pending = setTierMutation.isPending;
        let cls =
          "px-2.5 py-1 rounded-[5px] text-[12px] font-medium transition-colors border focus:outline-none";
        if (isActive) {
          cls +=
            tier === "trusted"
              ? " bg-status-ok/15 text-status-ok border-status-ok/30"
              : tier === "untrusted"
                ? " bg-status-danger/15 text-status-danger border-status-danger/30"
                : " bg-surface text-fg border-border";
        } else {
          cls += " bg-transparent text-fg-muted border-transparent hover:bg-surface hover:text-fg";
        }
        return (
          <button
            key={tier}
            className={cls}
            disabled={isActive || pending}
            onClick={() => setTierMutation.mutate(tier)}
            title={TIER_DESCRIPTIONS[tier]}
          >
            {TIER_LABELS[tier]}
          </button>
        );
      })}
      {setTierMutation.isError && (
        <span className="text-[11px] text-status-danger ml-1 flex items-center gap-0.5">
          <AlertTriangle style={{ width: 11, height: 11 }} />
          Failed
        </span>
      )}
      {setTierMutation.isSuccess && (
        <span className="text-[11px] text-status-ok ml-1 flex items-center gap-0.5">
          <CheckCircle2 style={{ width: 11, height: 11 }} />
          Saved
        </span>
      )}
    </div>
  );
}

// --- Main panel ---

export function RoleTrustPanel() {
  // Use the active workspace; fall back to "dogfood" for v1.
  // Follow-up CW-TODO: thread real workspace switching once UI plumbing lands.
  const rawWorkspaceID = useAppStore((s) => s.activeWorkspaceId);
  const workspaceID = rawWorkspaceID ?? "dogfood";

  // Fetch all agent profiles.
  const { data: agents = [], isLoading: agentsLoading } = useQuery({
    queryKey: ["agent-profiles"],
    queryFn: api.listAgents,
  });

  // Fetch workspace trust overrides for this workspace.
  const { data: trustData, isLoading: trustLoading } = useQuery({
    queryKey: ["workspace-role-trust", workspaceID],
    queryFn: () => api.listWorkspaceRoleTrust(workspaceID),
  });

  const overrideMap = new Map<string, TrustTier>(
    (trustData?.trust_overrides ?? []).map((o) => [o.agent_profile_id, o.trust_tier as TrustTier]),
  );

  const isLoading = agentsLoading || trustLoading;

  // Derive effective tier for each agent profile.
  // When no override is present, the profile's default_trust_tier from the
  // DB would be authoritative, but the list endpoint doesn't surface it.
  // v1: treat missing override as "normal" (safe default). A follow-up
  // should extend the API to return the agent's default_trust_tier.
  function effectiveTier(agent: AgentProfile): TrustTier {
    return overrideMap.get(agent.id) ?? "normal";
  }

  // Sort: internal first, then by name.
  const sorted = [...agents].sort((a, b) => {
    if (a.source === "internal" && b.source !== "internal") return -1;
    if (a.source !== "internal" && b.source === "internal") return 1;
    return a.name.localeCompare(b.name);
  });

  return (
    <div>
      <PanelHeader
        title="Role Trust"
        description="Set workspace-scoped trust tiers for agent roles. Trusted roles bypass approval gates (all bypasses are audit-logged). Untrusted roles are refused outright."
      />

      {!rawWorkspaceID && (
        <div className="mb-4 px-3 py-2 rounded-[7px] bg-status-warn/10 border border-status-warn/25 text-[12px] text-fg-muted">
          No active workspace selected. Showing trust settings for the{" "}
          <span className="font-mono text-fg">dogfood</span> workspace. Select a workspace to
          manage its trust configuration.
        </div>
      )}

      <SCard title="Agent Roles" meta={`workspace: ${workspaceID}`}>
        {isLoading ? (
          <div className="space-y-3 p-4">
            {[...Array(4)].map((_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : sorted.length === 0 ? (
          <div className="px-4 py-6 text-center text-[13px] text-fg-muted">
            No agent profiles found.
          </div>
        ) : (
          <div className="divide-y divide-border-subtle">
            {sorted.map((agent) => {
              const tier = effectiveTier(agent);
              return (
                <div
                  key={agent.id}
                  className="flex items-center gap-3 px-4 py-3 hover:bg-surface/40 transition-colors"
                >
                  {/* Avatar / icon */}
                  <div className="w-7 h-7 rounded-full bg-surface flex items-center justify-center shrink-0 text-[13px] font-semibold text-fg-secondary border border-border-subtle">
                    {agent.name.slice(0, 1).toUpperCase()}
                  </div>

                  {/* Identity */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-[13px] font-medium text-fg truncate">{agent.name}</span>
                      <span className="font-mono text-[10px] text-fg-faint bg-surface px-1.5 py-0.5 rounded border border-border-subtle">
                        {agent.slug}
                      </span>
                      {agent.source && agent.source !== "internal" && (
                        <span className="font-mono text-[10px] text-fg-muted">{agent.source}</span>
                      )}
                    </div>
                    {agent.description && (
                      <div className="text-[11px] text-fg-muted mt-0.5 truncate">
                        {agent.description}
                      </div>
                    )}
                  </div>

                  {/* Current tier badge */}
                  <div className="shrink-0 hidden sm:block">
                    <TierBadge tier={tier} />
                  </div>

                  {/* Tier selector */}
                  <div className="shrink-0">
                    <TierSelector
                      agentProfileID={agent.id}
                      currentTier={tier}
                      workspaceID={workspaceID}
                    />
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </SCard>

      <div className="mt-2 text-[11px] text-fg-faint leading-relaxed">
        Trust tiers are workspace-scoped. "Normal" removes any override and falls back to the
        agent profile's default tier. Changes take effect immediately for new dispatches.
      </div>
    </div>
  );
}
