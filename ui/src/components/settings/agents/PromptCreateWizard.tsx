import {
  ArrowLeft,
  ArrowRight,
  ChevronLeft,
  FileText,
  Loader2,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { DynamicIcon, IconPicker } from "@/components/ui/icon-picker";

interface PromptCreateWizardProps {
  onSubmit: (data: {
    name: string;
    slug: string;
    scope: "system" | "mode" | "skill" | "context";
    template: string;
    variables: string;
    priority: number;
    icon: string;
  }) => void;
  onCancel: () => void;
  isPending?: boolean;
}

type Step = "identity" | "content";
const STEPS: Step[] = ["identity", "content"];

const SCOPES = [
  { value: "system", label: "System" },
  { value: "mode", label: "Mode" },
  { value: "skill", label: "Skill" },
  { value: "context", label: "Context" },
] as const;

export function PromptCreateWizard({
  onSubmit,
  onCancel,
  isPending,
}: PromptCreateWizardProps) {
  const [step, setStep] = useState<Step>("identity");
  const nameRef = useRef<HTMLInputElement>(null);
  const promptRef = useRef<HTMLTextAreaElement>(null);

  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugManual, setSlugManual] = useState(false);
  const [scope, setScope] = useState<"system" | "mode" | "skill" | "context">("system");
  const [priority, setPriority] = useState(100);
  const [icon, setIcon] = useState("");
  const [template, setTemplate] = useState("");
  const [variables, setVariables] = useState("[]");

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

  useEffect(() => {
    if (step === "identity") setTimeout(() => nameRef.current?.focus(), 100);
    else if (step === "content") setTimeout(() => promptRef.current?.focus(), 100);
  }, [step]);

  const stepIndex = STEPS.indexOf(step);
  const isFirstStep = stepIndex === 0;
  const isLastStep = stepIndex === STEPS.length - 1;

  const canProceed = useMemo(() => {
    if (step === "identity") return name.trim().length > 0 && slug.trim().length > 0;
    if (step === "content") return template.trim().length > 0;
    return false;
  }, [step, name, slug, template]);

  const handleNext = useCallback(() => {
    if (!canProceed) return;
    if (isLastStep) {
      onSubmit({
        name: name.trim(),
        slug: slug.trim(),
        scope,
        template: template.trim(),
        variables,
        priority,
        icon,
      });
    } else {
      setStep(STEPS[stepIndex + 1]);
    }
  }, [canProceed, isLastStep, stepIndex, name, slug, scope, template, variables, priority, icon, onSubmit]);

  const handleBack = useCallback(() => {
    if (isFirstStep) onCancel();
    else setStep(STEPS[stepIndex - 1]);
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

  const inputClass =
    "w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-sm focus:outline-none focus:border-primary transition-colors";

  return (
    <div className="space-y-5" onKeyDown={handleKeyDown}>
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="icon" onClick={handleBack}>
          <ChevronLeft className="w-4 h-4" />
        </Button>
        <div className="flex-1 min-w-0">
          <h2 className="text-xl font-semibold text-fg">New Prompt</h2>
          <p className="text-xs text-fg-muted mt-0.5">
            {step === "identity" ? "Name and scope the prompt" : "Write the prompt template"}
          </p>
        </div>
      </div>

      <div className="flex items-center gap-2 px-1">
        {STEPS.map((_, i) => (
          <div key={i} className="flex-1">
            <div
              className={`h-1 rounded-full transition-colors ${
                i <= stepIndex ? "bg-primary" : "bg-surface"
              }`}
            />
          </div>
        ))}
      </div>

      <div className="min-h-[360px]">
        {step === "identity" && (
          <Card>
            <div className="p-5 space-y-5">
              <div className="flex items-start gap-4">
                <div className="shrink-0 flex flex-col items-center gap-1.5">
                  <div className="size-14 rounded-sm bg-surface flex items-center justify-center">
                    <DynamicIcon name={icon} className="w-7 h-7 text-fg-secondary" fallback={FileText} />
                  </div>
                  <IconPicker value={icon} onChange={setIcon} />
                </div>
                <div className="flex-1 space-y-3">
                  <div className="space-y-1">
                    <label className="text-xs font-medium text-fg-secondary">Name</label>
                    <input
                      ref={nameRef}
                      type="text"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      placeholder="My Prompt"
                      className={inputClass}
                    />
                  </div>
                  <div className="space-y-1">
                    <label className="text-xs font-medium text-fg-secondary">Slug</label>
                    <input
                      type="text"
                      value={slug}
                      onChange={(e) => { setSlug(e.target.value); setSlugManual(true); }}
                      placeholder="my-prompt"
                      className={`${inputClass} font-mono text-xs`}
                    />
                  </div>
                </div>
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-1">
                  <label className="text-xs font-medium text-fg-secondary">Scope</label>
                  <select
                    value={scope}
                    onChange={(e) => setScope(e.target.value as typeof scope)}
                    className={`${inputClass} appearance-none`}
                  >
                    {SCOPES.map((s) => (
                      <option key={s.value} value={s.value}>{s.label}</option>
                    ))}
                  </select>
                </div>
                <div className="space-y-1">
                  <label className="text-xs font-medium text-fg-secondary">Priority</label>
                  <input
                    type="number"
                    min="0"
                    value={priority}
                    onChange={(e) => setPriority(parseInt(e.target.value) || 0)}
                    className={inputClass}
                  />
                </div>
              </div>
            </div>
          </Card>
        )}

        {step === "content" && (
          <Card>
            <div className="p-5 space-y-4">
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <label className="text-xs font-medium text-fg-secondary">Prompt Template</label>
                  <span className="text-[10px] text-fg-faint tabular-nums">
                    {template.length.toLocaleString()} chars
                  </span>
                </div>
                <textarea
                  ref={promptRef}
                  value={template}
                  onChange={(e) => setTemplate(e.target.value)}
                  rows={12}
                  placeholder={"Enter the prompt content...\n\nUse {{variable_name}} for variables."}
                  className={`${inputClass} font-mono text-xs resize-y min-h-[240px] leading-relaxed`}
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-medium text-fg-secondary">
                  Variables (optional)
                </label>
                <textarea
                  value={variables}
                  onChange={(e) => setVariables(e.target.value)}
                  rows={4}
                  placeholder={`[{"name": "user_name", "type": "text", "required": true}]`}
                  className={`${inputClass} font-mono text-xs resize-y min-h-[80px]`}
                />
                <p className="text-[10px] text-fg-faint">
                  JSON array of variable definitions. Can be refined after creation.
                </p>
              </div>
            </div>
          </Card>
        )}
      </div>

      <div className="flex items-center justify-between pt-2 border-t border-border">
        <Button variant="ghost" onClick={handleBack} className="gap-1.5 text-xs">
          <ArrowLeft className="w-3.5 h-3.5" />
          {isFirstStep ? "Cancel" : "Back"}
        </Button>
        <div className="flex items-center gap-2">
          <span className="text-[10px] text-fg-faint">
            {isLastStep ? "Cmd+Enter to create" : "Cmd+Enter to continue"}
          </span>
          <Button onClick={handleNext} disabled={!canProceed || isPending} className="gap-1.5">
            {isPending ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
            ) : isLastStep ? (
              <>
                <FileText className="w-3.5 h-3.5" />
                Create Prompt
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

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden">
      {children}
    </div>
  );
}
