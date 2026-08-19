import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Bot,
  CircleDot,
  Database,
  GitFork,
  HardDrive,
  Layers,
  LifeBuoy,
  LockKeyhole,
  type LucideIcon,
  Play,
  RotateCcw,
} from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api } from "@/lib/api";
import {
  compactProviderModelLabel,
  deriveSidebarSessionSummary,
} from "@/lib/sidebar-session";
import type {
  DurableAgentInstance,
  DurableAgentSessionAttachmentState,
  SessionDetailsResponse,
} from "@/lib/types";

interface SessionDetailsPanelProps {
  sessionId: string | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onFork?: (details: SessionDetailsResponse, includeMessages: boolean) => void;
  onRestart?: (details: SessionDetailsResponse) => void;
  onRecover?: (details: SessionDetailsResponse) => void;
  onOpenStartFromHere?: (details: SessionDetailsResponse) => void;
}

export function SessionDetailsPanel({
  sessionId,
  open,
  onOpenChange,
  onFork,
  onRestart,
  onRecover,
  onOpenStartFromHere,
}: SessionDetailsPanelProps) {
  const detailsQuery = useQuery({
    queryKey: ["session-details", sessionId],
    queryFn: () => {
      if (!sessionId) throw new Error("session id is required");
      return api.getSessionDetails(sessionId);
    },
    enabled: open && !!sessionId,
  });

  const details = detailsQuery.data;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[86vh] overflow-hidden sm:max-w-2xl">
        <DialogHeader className="border-b border-divider px-5 pb-3 pt-5">
          <DialogTitle>Session details</DialogTitle>
          <DialogDescription className="sr-only">
            Read-only provider, runtime, mode, durable agent, and checkpoint
            details.
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-[calc(86vh-76px)] overflow-y-auto px-5 pb-5">
          {detailsQuery.isLoading && (
            <PanelState>Loading session details...</PanelState>
          )}
          {detailsQuery.isError && (
            <PanelState>Session details are unavailable.</PanelState>
          )}
          {!detailsQuery.isLoading && !detailsQuery.isError && !details && (
            <PanelState>No session details returned.</PanelState>
          )}
          {details && (
            <SessionDetailsBody
              details={details}
              onFork={onFork}
              onRestart={onRestart}
              onRecover={onRecover}
              onOpenStartFromHere={onOpenStartFromHere}
            />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function SessionDetailsBody({
  details,
  onFork,
  onRestart,
  onRecover,
  onOpenStartFromHere,
}: {
  details: SessionDetailsResponse;
  onFork?: (details: SessionDetailsResponse, includeMessages: boolean) => void;
  onRestart?: (details: SessionDetailsResponse) => void;
  onRecover?: (details: SessionDetailsResponse) => void;
  onOpenStartFromHere?: (details: SessionDetailsResponse) => void;
}) {
  const { session } = details;
  const summary = deriveSidebarSessionSummary(session);

  return (
    <div className="space-y-4 pt-4">
      <Section icon={CircleDot} title="Session">
        <FactGrid>
          <Fact
            label="Title"
            value={session.custom_name || session.title || "Untitled"}
          />
          <Fact label="Kind" value={summary.kindLabel} />
          <Fact
            label="Activity"
            value={formatLabel(details.activity_state)}
            mono
          />
          <Fact label="Short code" value={session.short_code} mono />
          <Fact label="Session ID" value={session.id} mono />
          <Fact label="Status" value={session.status} mono />
          <Fact
            label="Messages"
            value={String(session.message_count ?? 0)}
            mono
          />
          <Fact
            label="Last activity"
            value={formatDateTime(details.last_activity_at)}
            mono
          />
          <Fact
            label="Last useful work"
            value={formatDateTime(details.last_useful_activity_at)}
            mono
          />
          <Fact label="Project" value={session.project_id} mono />
        </FactGrid>
      </Section>

      <Section icon={LockKeyhole} title="Start facts">
        <FactGrid>
          <Fact
            label="Provider / model"
            value={compactProviderModelLabel(session.provider, session.model)}
          />
          <Fact label="Provider" value={session.provider} mono />
          <Fact label="Model" value={session.model} mono />
          <Fact
            label="Boot source"
            value={formatLabel(details.boot_source)}
            mono
          />
          <Fact
            label="Immutable fields"
            value={formatList(details.immutable_start_fields)}
            mono
          />
          <Fact label="Sidebar label" value={summary.metadataLabel} mono />
        </FactGrid>
      </Section>

      <Section icon={Bot} title="Persona">
        <FactGrid>
          <Fact
            label="Primary agent"
            value={
              details.primary_agent?.name ||
              details.primary_agent?.slug ||
              "None attached"
            }
          />
          <Fact label="Agent ID" value={details.primary_agent?.id} mono />
        </FactGrid>
      </Section>

      <Section icon={Activity} title="Runtime">
        {details.runtime?.state === "none" ? (
          <EmptyLine>
            Runtime state is none. This session has no resident runtime process.
          </EmptyLine>
        ) : null}
        <FactGrid>
          <Fact label="State" value={details.runtime?.state || "none"} mono />
          <Fact
            label="Runtime kind"
            value={details.runtime?.runtime_kind || "api"}
            mono
          />
          <Fact label="Runtime ID" value={details.runtime?.runtime_id} mono />
          <Fact label="PID" value={formatNumber(details.runtime?.pid)} mono />
          <Fact label="Provider" value={details.runtime?.provider} mono />
          <Fact
            label="Provider session"
            value={details.runtime?.provider_session_id}
            mono
          />
          <Fact
            label="Started"
            value={formatDateTime(details.runtime?.started_at)}
            mono
          />
          <Fact
            label="Updated"
            value={formatDateTime(details.runtime?.updated_at)}
            mono
          />
          <Fact
            label="Workdir"
            value={details.runtime?.workspace_dir}
            mono
            wide
          />
          <Fact label="Boot dir" value={details.runtime?.boot_dir} mono wide />
          {details.runtime?.failure_reason ? (
            <Fact
              label="Failure reason"
              value={details.runtime.failure_reason}
              wide
            />
          ) : null}
        </FactGrid>
      </Section>

      <Section icon={Activity} title="Observability">
        <FactGrid>
          <Fact
            label="Activity state"
            value={formatLabel(details.activity_state)}
            mono
          />
          <Fact
            label="Halt state"
            value={details.halt?.is_halted ? "halted" : "healthy"}
            mono
          />
          <Fact
            label="Halted at"
            value={formatDateTime(details.halt?.halted_at)}
            mono
          />
          <Fact label="Halt reason" value={details.halt?.halted_reason} wide />
          <Fact
            label="Usage tokens"
            value={formatNumber(details.usage?.total_tokens)}
            mono
          />
          <Fact
            label="Estimated cost"
            value={formatCurrency(details.usage?.estimated_cost_usd)}
            mono
          />
          <Fact
            label="Usage rows"
            value={formatNumber(details.usage?.message_count)}
            mono
          />
          <Fact
            label="Cache read tokens"
            value={formatNumber(details.usage?.cache_read_tokens)}
            mono
          />
        </FactGrid>
      </Section>

      <Section icon={HardDrive} title="Durable agent">
        {!details.current_durable_agent &&
        details.durable_attachments.length === 0 ? (
          <EmptyLine>No durable agent is attached to this session.</EmptyLine>
        ) : null}
        {details.current_durable_agent ? (
          <DurableAgentFacts instance={details.current_durable_agent} />
        ) : null}
        {details.durable_attachments.length > 0 ? (
          <div className="mt-3 space-y-2">
            {details.durable_attachments.map((attachment) => (
              <AttachmentRow
                key={`${attachment.instance_id}:${attachment.relation}`}
                attachment={attachment}
              />
            ))}
          </div>
        ) : null}
        {details.recent_durable_events.length > 0 ? (
          <div className="mt-3 space-y-2">
            {details.recent_durable_events.map((event) => (
              <div
                key={event.id}
                className="rounded-[6px] border border-border-subtle bg-bg px-3 py-2"
              >
                <div className="flex items-center justify-between gap-3">
                  <span className="font-mono text-[11px] text-fg-secondary">
                    {event.event_type}
                  </span>
                  <span className="text-[11px] text-fg-faint">
                    {formatDateTime(event.created_at)}
                  </span>
                </div>
                {event.message ? (
                  <p className="mt-1 text-[12px] text-fg-secondary">
                    {event.message}
                  </p>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}
      </Section>

      <Section icon={Database} title="Checkpoint">
        <FactGrid>
          <Fact
            label="Status"
            value={details.checkpoint?.status || "not_available"}
            mono
          />
        </FactGrid>
      </Section>

      <Section icon={Layers} title="Read-only">
        <div className="space-y-3">
          <EmptyLine>
            Provider, model, runtime, and durable-agent facts are locked to this
            session. Changing them creates a new session boundary.
          </EmptyLine>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              variant="secondary"
              onClick={() => onFork?.(details, true)}
              disabled={!onFork}
              className="gap-1.5"
            >
              <GitFork className="size-3.5" />
              Fork with history
            </Button>
            <Button
              type="button"
              size="sm"
              variant="secondary"
              onClick={() => onRestart?.(details)}
              disabled={!onRestart}
              className="gap-1.5"
            >
              <RotateCcw className="size-3.5" />
              Restart as new session
            </Button>
            <Button
              type="button"
              size="sm"
              variant="secondary"
              onClick={() => onRecover?.(details)}
              disabled={!onRecover}
              className="gap-1.5"
              title="Resume this session after a restart — the next message reloads prior context (recovery pack + provider resume)"
            >
              <LifeBuoy className="size-3.5" />
              Recover session
            </Button>
            <Button
              type="button"
              size="sm"
              variant="secondary"
              onClick={() => onOpenStartFromHere?.(details)}
              disabled={!onOpenStartFromHere}
              className="gap-1.5"
            >
              <Play className="size-3.5" />
              Open Start from here
            </Button>
          </div>
        </div>
      </Section>
    </div>
  );
}

function DurableAgentFacts({ instance }: { instance: DurableAgentInstance }) {
  return (
    <FactGrid>
      <Fact
        label="Current instance"
        value={instance.name || instance.slug || instance.id}
      />
      <Fact label="Instance ID" value={instance.id} mono />
      <Fact label="Lifecycle class" value={instance.lifecycle_class} mono />
      <Fact label="Status" value={instance.status} mono />
      <Fact label="Runtime kind" value={instance.runtime_kind} mono />
      <Fact label="Launch source" value={instance.launch_source_type} mono />
      <Fact label="Current session" value={instance.current_session_id} mono />
      <Fact label="Work root" value={instance.work_root} mono wide />
      {instance.failure_reason ? (
        <Fact label="Failure reason" value={instance.failure_reason} wide />
      ) : null}
    </FactGrid>
  );
}

function AttachmentRow({
  attachment,
}: {
  attachment: DurableAgentSessionAttachmentState;
}) {
  return (
    <div className="rounded-[6px] border border-border-subtle bg-bg px-3 py-2">
      <div className="flex items-center justify-between gap-3">
        <span className="text-[12px] font-medium text-fg">Attachment</span>
        <span className="font-mono text-[11px] text-fg-muted">
          {attachment.relation}
        </span>
      </div>
      <div className="mt-1 grid gap-1 font-mono text-[11px] text-fg-muted sm:grid-cols-2">
        <span className="truncate">
          instance {fallback(attachment.instance_id)}
        </span>
        <span className="truncate">
          runtime {fallback(attachment.runtime_state)}
        </span>
        {attachment.runtime_failure_reason ? (
          <span className="truncate sm:col-span-2">
            failure {fallback(attachment.runtime_failure_reason)}
          </span>
        ) : null}
        {attachment.halted_reason ? (
          <span className="truncate sm:col-span-2">
            halt {fallback(attachment.halted_reason)}
          </span>
        ) : null}
      </div>
    </div>
  );
}

function Section({
  icon: Icon,
  title,
  children,
}: {
  icon: LucideIcon;
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="rounded-[8px] border border-border-subtle bg-bg-elevated">
      <div className="flex items-center gap-2 border-b border-divider px-3 py-2">
        <Icon className="h-3.5 w-3.5 text-fg-muted" />
        <h3 className="text-[12px] font-semibold text-fg">{title}</h3>
      </div>
      <div className="px-3 py-3">{children}</div>
    </section>
  );
}

function FactGrid({ children }: { children: ReactNode }) {
  return <dl className="grid gap-2 sm:grid-cols-2">{children}</dl>;
}

function Fact({
  label,
  value,
  mono = false,
  wide = false,
}: {
  label: string;
  value?: string | null;
  mono?: boolean;
  wide?: boolean;
}) {
  return (
    <div className={wide ? "sm:col-span-2" : undefined}>
      <dt className="font-mono text-[10px] font-semibold uppercase tracking-[0.14em] text-fg-muted">
        {label}
      </dt>
      <dd
        className={`mt-1 min-h-5 break-words rounded-[6px] border border-border-subtle bg-bg px-2 py-1 text-[12px] text-fg ${
          mono ? "font-mono" : ""
        }`}
      >
        {fallback(value)}
      </dd>
    </div>
  );
}

function PanelState({ children }: { children: ReactNode }) {
  return (
    <div className="py-12 text-center text-[13px] text-fg-muted">
      {children}
    </div>
  );
}

function EmptyLine({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-[6px] border border-border-subtle bg-bg px-3 py-2 text-[12px] text-fg-muted">
      {children}
    </p>
  );
}

function fallback(value?: string | null): string {
  if (value === undefined || value === null || value === "") return "None";
  return value;
}

function formatNumber(value?: number): string {
  return typeof value === "number" && Number.isFinite(value)
    ? String(value)
    : "None";
}

function formatCurrency(value?: number | null): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "None";
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 4,
    maximumFractionDigits: 4,
  }).format(value);
}

function formatDateTime(value?: string | null): string {
  if (!value) return "None";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function formatList(values: string[]): string {
  return values.length > 0 ? values.map(formatLabel).join(", ") : "None";
}

function formatLabel(value: string): string {
  if (!value) return "None";
  return value.replace(/_/g, " ");
}
