import {
  ArrowLeft,
  ArrowRight,
  ChevronLeft,
  Loader2,
  Wrench,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { DynamicIcon, IconPicker } from "@/components/ui/icon-picker";

interface SkillCreateWizardProps {
  categories: readonly string[];
  onSubmit: (data: {
    name: string;
    slug: string;
    category: string;
    description: string;
    icon: string;
    tool_bindings: string;
    input_schema: string;
    settings: string;
  }) => void;
  onCancel: () => void;
  isPending?: boolean;
}

type Step = "identity" | "config";
const STEPS: Step[] = ["identity", "config"];

export function SkillCreateWizard({
  categories,
  onSubmit,
  onCancel,
  isPending,
}: SkillCreateWizardProps) {
  const [step, setStep] = useState<Step>("identity");
  const nameRef = useRef<HTMLInputElement>(null);

  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugManual, setSlugManual] = useState(false);
  const [category, setCategory] = useState(categories[0] ?? "general");
  const [description, setDescription] = useState("");
  const [icon, setIcon] = useState("");
  const [toolBindings, setToolBindings] = useState("[]");
  const [inputSchema, setInputSchema] = useState("{}");

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
  }, [step]);

  const stepIndex = STEPS.indexOf(step);
  const isFirstStep = stepIndex === 0;
  const isLastStep = stepIndex === STEPS.length - 1;

  const canProceed = useMemo(() => {
    if (step === "identity") return name.trim().length > 0 && slug.trim().length > 0;
    return true;
  }, [step, name, slug]);

  const handleNext = useCallback(() => {
    if (!canProceed) return;
    if (isLastStep) {
      onSubmit({
        name: name.trim(),
        slug: slug.trim(),
        category,
        description: description.trim(),
        icon,
        tool_bindings: toolBindings,
        input_schema: inputSchema,
        settings: "{}",
      });
    } else {
      setStep(STEPS[stepIndex + 1]);
    }
  }, [canProceed, isLastStep, stepIndex, name, slug, category, description, icon, toolBindings, inputSchema, onSubmit]);

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
          <h2 className="text-xl font-semibold text-fg">New Skill</h2>
          <p className="text-xs text-fg-muted mt-0.5">
            {step === "identity" ? "Name and categorize the skill" : "Configure tool bindings"}
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

      <div className="min-h-[320px]">
        {step === "identity" && (
          <Card>
            <div className="p-5 space-y-5">
              <div className="flex items-start gap-4">
                <div className="shrink-0 flex flex-col items-center gap-1.5">
                  <div className="size-14 rounded-sm bg-surface flex items-center justify-center">
                    <DynamicIcon name={icon} className="w-7 h-7 text-fg-secondary" fallback={Wrench} />
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
                      placeholder="My Skill"
                      className={inputClass}
                    />
                  </div>
                  <div className="space-y-1">
                    <label className="text-xs font-medium text-fg-secondary">Slug</label>
                    <input
                      type="text"
                      value={slug}
                      onChange={(e) => { setSlug(e.target.value); setSlugManual(true); }}
                      placeholder="my-skill"
                      className={`${inputClass} font-mono text-xs`}
                    />
                  </div>
                </div>
              </div>

              <div className="space-y-1">
                <label className="text-xs font-medium text-fg-secondary">Category</label>
                <select
                  value={category}
                  onChange={(e) => setCategory(e.target.value)}
                  className={`${inputClass} appearance-none`}
                >
                  {categories.map((c) => (
                    <option key={c} value={c}>
                      {c.charAt(0).toUpperCase() + c.slice(1)}
                    </option>
                  ))}
                </select>
              </div>

              <div className="space-y-1">
                <label className="text-xs font-medium text-fg-secondary">Description</label>
                <input
                  type="text"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Brief description of what this skill does"
                  className={inputClass}
                />
              </div>
            </div>
          </Card>
        )}

        {step === "config" && (
          <Card>
            <div className="p-5 space-y-4">
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-fg-secondary">Tool Bindings</label>
                <textarea
                  value={toolBindings}
                  onChange={(e) => setToolBindings(e.target.value)}
                  rows={6}
                  placeholder='[{"server": "filesystem", "tool": "read_file"}]'
                  className={`${inputClass} font-mono text-xs resize-y min-h-[100px]`}
                />
                <p className="text-[10px] text-fg-faint">
                  JSON array of {`{server, tool}`} objects
                </p>
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-medium text-fg-secondary">Input Schema (optional)</label>
                <textarea
                  value={inputSchema}
                  onChange={(e) => setInputSchema(e.target.value)}
                  rows={6}
                  placeholder='{"properties": {"query": {"type": "string"}}}'
                  className={`${inputClass} font-mono text-xs resize-y min-h-[100px]`}
                />
                <p className="text-[10px] text-fg-faint">
                  JSON schema for the skill's input parameters. Can be refined after creation.
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
                <Wrench className="w-3.5 h-3.5" />
                Create Skill
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
    <div className="rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden">
      {children}
    </div>
  );
}
