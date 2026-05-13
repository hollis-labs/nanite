import {
  ArrowLeft,
  ArrowRight,
  ChevronLeft,
  Loader2,
  Sparkles,
  User,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { DynamicIcon, IconPicker } from "@/components/ui/icon-picker";
import type { AgentProfile } from "@/lib/types";

// ─── Types ──────────────────────────────────────────────────────────

interface AgentCreateWizardProps {
  modelOptions: { id: string; label: string }[];
  defaultModel: string;
  onSubmit: (data: Omit<AgentProfile, "id" | "created_at" | "updated_at" | "agent_hash" | "version">) => void;
  onCancel: () => void;
  isPending?: boolean;
  /** Called with the new agent's ID after creation succeeds */
  createdAgentId?: string;
}

type Step = "identity" | "instructions";
const STEPS: Step[] = ["identity", "instructions"];

// ─── Component ──────────────────────────────────────────────────────

export function AgentCreateWizard({
  modelOptions,
  defaultModel,
  onSubmit,
  onCancel,
  isPending,
}: AgentCreateWizardProps) {
  const [step, setStep] = useState<Step>("identity");
  const nameRef = useRef<HTMLInputElement>(null);
  const promptRef = useRef<HTMLTextAreaElement>(null);

  // Form state
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugManual, setSlugManual] = useState(false);
  const [description, setDescription] = useState("");
  const [icon, setIcon] = useState("");
  const [avatar, setAvatar] = useState("");
  const [defaultModelValue, setDefaultModelValue] = useState(defaultModel);
  const [canExecute, setCanExecute] = useState(false);
  const [systemPrompt, setSystemPrompt] = useState("");

  // Auto-generate slug from name unless manually edited
  useEffect(() => {
    if (!slugManual && name) {
      setSlug(
        name
          .toLowerCase()
          .replace(/[^a-z0-9\s-]/g, "")
          .replace(/\s+/g, "-")
          .replace(/-+/g, "-")
          .slice(0, 64),
      );
    }
  }, [name, slugManual]);

  // Focus management
  useEffect(() => {
    if (step === "identity") {
      setTimeout(() => nameRef.current?.focus(), 100);
    } else if (step === "instructions") {
      setTimeout(() => promptRef.current?.focus(), 100);
    }
  }, [step]);

  const stepIndex = STEPS.indexOf(step);
  const isFirstStep = stepIndex === 0;
  const isLastStep = stepIndex === STEPS.length - 1;

  const canProceed = useMemo(() => {
    if (step === "identity") return name.trim().length > 0 && slug.trim().length > 0;
    if (step === "instructions") return systemPrompt.trim().length > 0;
    return false;
  }, [step, name, slug, systemPrompt]);

  const handleNext = useCallback(() => {
    if (!canProceed) return;
    if (isLastStep) {
      onSubmit({
        name: name.trim(),
        slug: slug.trim(),
        avatar,
        icon,
        description: description.trim(),
        system_prompt: systemPrompt.trim(),
        default_model: defaultModelValue,
        can_execute: canExecute,
        mcp_servers: "[]",
        tool_permissions: "{}",
        modes: "",
        settings: "{}",
        tools: "[]",
        directories: "[]",
        constraints: "{}",
        tags: "[]",
        status: "active",
        source: "api",
        source_ref: "",
      });
    } else {
      setStep(STEPS[stepIndex + 1]);
    }
  }, [canProceed, isLastStep, stepIndex, name, slug, avatar, icon, description, systemPrompt, defaultModelValue, canExecute, onSubmit]);

  const handleBack = useCallback(() => {
    if (isFirstStep) {
      onCancel();
    } else {
      setStep(STEPS[stepIndex - 1]);
    }
  }, [isFirstStep, stepIndex, onCancel]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && canProceed) {
        e.preventDefault();
        handleNext();
      }
    },
    [canProceed, handleNext],
  );

  // ─── Render ───────────────────────────────────────────────────────

  return (
    <div className="space-y-5" onKeyDown={handleKeyDown}>
      {/* Header */}
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="icon" onClick={handleBack}>
          <ChevronLeft className="w-4 h-4" />
        </Button>
        <div className="flex-1 min-w-0">
          <h2 className="text-xl font-semibold text-fg">New Agent</h2>
          <p className="text-xs text-fg-muted mt-0.5">
            {step === "identity" ? "Define who this agent is" : "Write the system prompt"}
          </p>
        </div>
      </div>

      {/* Progress indicator */}
      <div className="flex items-center gap-2 px-1">
        {STEPS.map((s, i) => (
          <div key={s} className="flex items-center gap-2 flex-1">
            <div
              className={`h-1 flex-1 rounded-full transition-colors ${
                i <= stepIndex ? "bg-primary" : "bg-surface"
              }`}
            />
          </div>
        ))}
      </div>

      {/* Step content */}
      <div className="min-h-[360px]">
        {step === "identity" && (
          <IdentityStep
            name={name}
            onNameChange={setName}
            slug={slug}
            onSlugChange={(v) => { setSlug(v); setSlugManual(true); }}
            description={description}
            onDescriptionChange={setDescription}
            icon={icon}
            onIconChange={setIcon}
            avatar={avatar}
            onAvatarChange={setAvatar}
            model={defaultModelValue}
            onModelChange={setDefaultModelValue}
            modelOptions={modelOptions}
            canExecute={canExecute}
            onCanExecuteChange={setCanExecute}
            nameRef={nameRef}
          />
        )}

        {step === "instructions" && (
          <InstructionsStep
            name={name}
            icon={icon}
            avatar={avatar}
            systemPrompt={systemPrompt}
            onSystemPromptChange={setSystemPrompt}
            promptRef={promptRef}
          />
        )}
      </div>

      {/* Footer — navigation */}
      <div className="flex items-center justify-between pt-2 border-t border-border">
        <Button variant="ghost" onClick={handleBack} className="gap-1.5 text-xs">
          <ArrowLeft className="w-3.5 h-3.5" />
          {isFirstStep ? "Cancel" : "Back"}
        </Button>
        <div className="flex items-center gap-2">
          <span className="text-[10px] text-fg-faint">
            {isLastStep ? "Cmd+Enter to create" : "Cmd+Enter to continue"}
          </span>
          <Button
            onClick={handleNext}
            disabled={!canProceed || isPending}
            className="gap-1.5"
          >
            {isPending ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
            ) : isLastStep ? (
              <>
                <Sparkles className="w-3.5 h-3.5" />
                Create Agent
              </>
            ) : (
              <>
                Continue
                <ArrowRight className="w-3.5 h-3.5" />
              </>
            )}
          </Button>
        </div>
      </div>
    </div>
  );
}

// ─── Step 1: Identity ───────────────────────────────────────────────

function IdentityStep({
  name,
  onNameChange,
  slug,
  onSlugChange,
  description,
  onDescriptionChange,
  icon,
  onIconChange,
  avatar,
  onAvatarChange,
  model,
  onModelChange,
  modelOptions,
  canExecute,
  onCanExecuteChange,
  nameRef,
}: {
  name: string;
  onNameChange: (v: string) => void;
  slug: string;
  onSlugChange: (v: string) => void;
  description: string;
  onDescriptionChange: (v: string) => void;
  icon: string;
  onIconChange: (v: string) => void;
  avatar: string;
  onAvatarChange: (v: string) => void;
  model: string;
  onModelChange: (v: string) => void;
  modelOptions: { id: string; label: string }[];
  canExecute: boolean;
  onCanExecuteChange: (v: boolean) => void;
  nameRef: React.RefObject<HTMLInputElement | null>;
}) {
  const inputClass =
    "w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-sm focus:outline-none focus:border-primary transition-colors";

  return (
    <Card>
      <div className="p-5 space-y-5">
        {/* Avatar preview + name */}
        <div className="flex items-start gap-4">
          <div className="shrink-0 flex flex-col items-center gap-1.5">
            <div className="size-14 rounded-sm bg-surface flex items-center justify-center">
              {icon ? (
                <DynamicIcon name={icon} className="w-7 h-7 text-fg-secondary" fallback={User} />
              ) : avatar ? (
                <span className="text-2xl">{avatar}</span>
              ) : (
                <User className="w-7 h-7 text-fg-muted" />
              )}
            </div>
            <IconPicker value={icon} onChange={onIconChange} />
          </div>
          <div className="flex-1 space-y-3">
            <div className="space-y-1">
              <label className="text-xs font-medium text-fg-secondary">Name</label>
              <input
                ref={nameRef}
                type="text"
                value={name}
                onChange={(e) => onNameChange(e.target.value)}
                placeholder="My Agent"
                className={inputClass}
              />
            </div>
            <div className="space-y-1">
              <label className="text-xs font-medium text-fg-secondary">Slug</label>
              <input
                type="text"
                value={slug}
                onChange={(e) => onSlugChange(e.target.value)}
                placeholder="my-agent"
                className={`${inputClass} font-mono text-xs`}
              />
              <p className="text-[10px] text-fg-faint">Auto-generated from name. Edit to customize.</p>
            </div>
          </div>
        </div>

        {/* Description */}
        <div className="space-y-1">
          <label className="text-xs font-medium text-fg-secondary">Description</label>
          <input
            type="text"
            value={description}
            onChange={(e) => onDescriptionChange(e.target.value)}
            placeholder="What does this agent do?"
            className={inputClass}
          />
        </div>

        {/* Model + options row */}
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1">
            <label className="text-xs font-medium text-fg-secondary">Default Model</label>
            <select
              value={model}
              onChange={(e) => onModelChange(e.target.value)}
              className={`${inputClass} appearance-none`}
            >
              {modelOptions.map((m) => (
                <option key={m.id} value={m.id}>{m.label}</option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium text-fg-secondary">Avatar Emoji</label>
            <input
              type="text"
              value={avatar}
              onChange={(e) => onAvatarChange(e.target.value)}
              placeholder="🤖"
              maxLength={4}
              className={`${inputClass} text-center text-lg`}
            />
          </div>
        </div>

        {/* Can Execute toggle */}
        <div
          className="flex items-center justify-between px-3 py-2.5 rounded-lg bg-bg-elevated border border-border-subtle cursor-pointer hover:bg-surface/40 transition-colors"
          onClick={() => onCanExecuteChange(!canExecute)}
        >
          <div>
            <span className="text-sm text-fg">Can Execute Tools</span>
            <p className="text-[11px] text-fg-muted mt-0.5">Allow this agent to run MCP tools and CLI commands</p>
          </div>
          <div
            className={`w-9 h-5 rounded-full transition-colors relative ${
              canExecute ? "bg-toggle-on" : "bg-surface-hover"
            }`}
          >
            <div
              className={`absolute top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${
                canExecute ? "translate-x-4" : "translate-x-0.5"
              }`}
            />
          </div>
        </div>
      </div>
    </Card>
  );
}

// ─── Step 2: Instructions ───────────────────────────────────────────

function InstructionsStep({
  name,
  icon,
  avatar,
  systemPrompt,
  onSystemPromptChange,
  promptRef,
}: {
  name: string;
  icon: string;
  avatar: string;
  systemPrompt: string;
  onSystemPromptChange: (v: string) => void;
  promptRef: React.RefObject<HTMLTextAreaElement | null>;
}) {
  return (
    <Card>
      <div className="p-5 space-y-4">
        {/* Context reminder */}
        <div className="flex items-center gap-2.5">
          <div className="size-8 rounded-sm bg-surface flex items-center justify-center shrink-0">
            {icon ? (
              <DynamicIcon name={icon} className="w-4 h-4 text-fg-secondary" fallback={User} />
            ) : avatar ? (
              <span className="text-sm">{avatar}</span>
            ) : (
              <User className="w-4 h-4 text-fg-muted" />
            )}
          </div>
          <div>
            <span className="text-sm font-medium text-fg">{name}</span>
            <p className="text-[11px] text-fg-muted">System prompt — this defines the agent's behavior and personality</p>
          </div>
        </div>

        {/* Prompt editor */}
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <label className="text-xs font-medium text-fg-secondary">System Prompt</label>
            <span className="text-[10px] text-fg-faint tabular-nums">
              {systemPrompt.length.toLocaleString()} chars
            </span>
          </div>
          <textarea
            ref={promptRef}
            value={systemPrompt}
            onChange={(e) => onSystemPromptChange(e.target.value)}
            rows={14}
            placeholder={"You are a helpful assistant that...\n\nYour responsibilities:\n- ...\n- ...\n\nRules:\n- ..."}
            className="w-full px-3 py-2.5 bg-bg-elevated border border-border-subtle rounded-lg text-sm text-fg font-mono leading-relaxed focus:outline-none focus:border-primary resize-y min-h-[280px] placeholder:text-fg-faint/50 transition-colors"
          />
          <p className="text-[10px] text-fg-faint">
            You can refine this later. Advanced config (tools, directories, MCP servers) is available after creation.
          </p>
        </div>
      </div>
    </Card>
  );
}

// ─── Shared ─────────────────────────────────────────────────────────

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden">
      {children}
    </div>
  );
}
