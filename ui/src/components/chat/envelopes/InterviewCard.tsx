import { useState, useRef, useEffect, useCallback } from "react";
import { X, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { type Answer, ResponseStatus } from "@/lib/envelope-response";
import type { Envelope, Question } from "@/lib/types";
import type { EnvelopeResponder } from "./EnvelopeRenderer";

// Normalize an option value: string | {value, label, description?} → {value, label, description?}
function normalizeOption(raw: unknown): { value: string; label: string; description?: string } {
  if (typeof raw === "string") return { value: raw, label: raw };
  if (raw && typeof raw === "object" && "value" in raw) {
    const o = raw as { value: string; label?: string; description?: string };
    return { value: o.value, label: o.label ?? o.value, description: o.description };
  }
  return { value: String(raw), label: String(raw) };
}

interface InterviewCardProps {
  envelope: Envelope;
  onRespond?: EnvelopeResponder;
  userMessageCount?: number;
}

export function InterviewCard({ envelope, onRespond, userMessageCount }: InterviewCardProps) {
  const questions = envelope.questions ?? [];
  const alreadyAnswered = envelope.prior_response != null;

  const [answers, setAnswers] = useState<Record<number, string | string[]>>(() => {
    const initial: Record<number, string | string[]> = {};
    questions.forEach((q, i) => {
      if (q.type === "checkbox") {
        initial[i] = q.default ? [q.default] : [];
      } else {
        initial[i] = q.default ?? "";
      }
    });
    return initial;
  });
  const [errors, setErrors] = useState<Record<number, string>>({});
  const [dismissed, setDismissed] = useState(false);
  const [submitted, setSubmitted] = useState(alreadyAnswered);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const mountCountRef = useRef(userMessageCount);
  useEffect(() => {
    if (userMessageCount !== undefined && mountCountRef.current !== undefined) {
      if (userMessageCount > mountCountRef.current && !submitted) {
        setDismissed(true);
      }
    }
  }, [userMessageCount, submitted]);

  const validate = useCallback((): boolean => {
    const next: Record<number, string> = {};
    questions.forEach((q, i) => {
      if (!q.required) return;
      const val = answers[i];
      const empty = Array.isArray(val) ? val.length === 0 : !val;
      if (empty) next[i] = "Required";
    });
    setErrors(next);
    return Object.keys(next).length === 0;
  }, [questions, answers]);

  const buildAnswers = useCallback(
    (overrideWithDefaults = false): Answer[] =>
      questions.map((q, i) => ({
        questionId: `q-${i}`,
        value: overrideWithDefaults
          ? (answers[i] || q.default || "")
          : (answers[i] ?? ""),
      })),
    [questions, answers],
  );

  const handleSubmit = useCallback(async () => {
    if (!validate()) return;
    setSubmitError(null);
    const typed = buildAnswers();
    setSubmitted(true);
    if (!onRespond) return;
    try {
      await onRespond({ status: ResponseStatus.Submitted, answers: typed });
    } catch (err) {
      setSubmitted(false);
      setSubmitError(err instanceof Error ? err.message : "Failed to submit");
    }
  }, [validate, buildAnswers, onRespond]);

  const handleAcceptSuggested = useCallback(async () => {
    setSubmitError(null);
    const typed = buildAnswers(true);
    setSubmitted(true);
    if (!onRespond) return;
    try {
      await onRespond({ status: ResponseStatus.Submitted, answers: typed });
    } catch (err) {
      setSubmitted(false);
      setSubmitError(err instanceof Error ? err.message : "Failed to submit");
    }
  }, [buildAnswers, onRespond]);

  const hasDefaults = questions.some((q) => q.default != null && q.default !== "");
  const allRequiredHaveDefaults = questions
    .filter((q) => q.required)
    .every((q) => q.default != null && q.default !== "");

  if (dismissed) return null;

  if (submitted) {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-3">
        <p className="text-xs text-success">Answers submitted</p>
      </div>
    );
  }

  return (
    <div className="rounded-md border border-border-subtle bg-bg-elevated">
      {(envelope.title || envelope.subtitle) ? (
        <div className="flex items-start justify-between px-3 pt-3 pb-2 border-b border-border-subtle/50">
          <div className="flex-1 min-w-0 pr-2">
            {envelope.title && (
              <p className="text-sm font-semibold text-fg leading-snug">{envelope.title}</p>
            )}
            {envelope.subtitle && (
              <p className="text-xs text-fg-muted mt-0.5 leading-relaxed">{envelope.subtitle}</p>
            )}
          </div>
          <button
            type="button"
            onClick={() => setDismissed(true)}
            className="p-0.5 text-fg-faint hover:text-fg-muted transition-colors shrink-0"
            aria-label="Dismiss"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      ) : (
        <div className="flex justify-end px-2 pt-2">
          <button
            type="button"
            onClick={() => setDismissed(true)}
            className="p-0.5 text-fg-faint hover:text-fg-muted transition-colors"
            aria-label="Dismiss"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      )}

      <div className="px-3 py-3 space-y-4">
        {questions.map((q, i) => (
          <QuestionInput
            key={i}
            question={q}
            answer={answers[i] ?? ""}
            error={errors[i]}
            onChange={(val) => {
              setAnswers((a) => ({ ...a, [i]: val }));
              if (errors[i]) setErrors((e) => { const n = { ...e }; delete n[i]; return n; });
            }}
          />
        ))}
      </div>

      <div className="px-3 pb-3">
        {submitError && (
          <p className="text-xs text-danger mb-2" role="alert">{submitError}</p>
        )}
        <div className="flex items-center justify-between gap-2">
          <div>
            {hasDefaults && allRequiredHaveDefaults && (
              <button
                type="button"
                onClick={() => void handleAcceptSuggested()}
                className="text-xs text-fg-muted hover:text-fg transition-colors"
              >
                Accept suggested
              </button>
            )}
          </div>
          <Button
            size="sm"
            className="bg-primary hover:bg-primary-hover text-white text-xs px-4 py-1 h-7"
            onClick={() => void handleSubmit()}
          >
            Submit
          </Button>
        </div>
      </div>
    </div>
  );
}

interface QuestionInputProps {
  question: Question;
  answer: string | string[];
  error?: string;
  onChange: (val: string | string[]) => void;
}

function QuestionInput({ question, answer, error, onChange }: QuestionInputProps) {
  const isCard = question.display_style === "card";

  return (
    <div>
      <label className="block text-xs font-medium text-fg-secondary mb-1">
        {question.prompt}
        {question.required && <span className="text-danger ml-0.5">*</span>}
      </label>

      {question.description && (
        <p className="text-xs text-fg-muted mb-2 leading-relaxed">{question.description}</p>
      )}

      {question.type === "text" && (
        <input
          type="text"
          value={String(answer ?? "")}
          onChange={(e) => onChange(e.target.value)}
          className="w-full bg-bg border border-border rounded px-2 py-1 text-[11px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
        />
      )}

      {question.type === "textarea" && (
        <textarea
          value={String(answer ?? "")}
          onChange={(e) => onChange(e.target.value)}
          rows={3}
          className="w-full bg-bg border border-border rounded px-2 py-1 text-[11px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary resize-none"
        />
      )}

      {question.type === "select" && question.options && (
        <select
          value={String(answer ?? "")}
          onChange={(e) => onChange(e.target.value)}
          className="w-full bg-bg border border-border rounded px-2 py-1 text-[11px] text-fg focus:outline-none focus:ring-1 focus:ring-primary"
        >
          <option value="">Select…</option>
          {question.options.map((raw) => {
            const opt = normalizeOption(raw);
            return <option key={opt.value} value={opt.value}>{opt.label}</option>;
          })}
        </select>
      )}

      {question.type === "radio" && question.options && (
        isCard
          ? <CardOptions
              options={question.options}
              selected={String(answer ?? "")}
              defaultValue={question.default}
              multi={false}
              onChange={(v) => onChange(v as string)}
            />
          : <CompactRadioOptions
              options={question.options}
              selected={String(answer ?? "")}
              defaultValue={question.default}
              onChange={(v) => onChange(v)}
            />
      )}

      {question.type === "checkbox" && question.options && (
        isCard
          ? <CardOptions
              options={question.options}
              selected={Array.isArray(answer) ? answer : []}
              defaultValue={question.default}
              multi={true}
              onChange={(v) => onChange(v as string[])}
            />
          : <CompactCheckboxOptions
              options={question.options}
              selected={Array.isArray(answer) ? answer : []}
              defaultValue={question.default}
              onChange={(v) => onChange(v)}
            />
      )}

      {error && <p className="text-xs text-danger mt-1">{error}</p>}
    </div>
  );
}

interface CompactRadioOptionsProps {
  options: Question["options"];
  selected: string;
  defaultValue?: string;
  onChange: (v: string) => void;
}

function CompactRadioOptions({ options = [], selected, defaultValue, onChange }: CompactRadioOptionsProps) {
  return (
    <div className="space-y-1.5">
      {options.map((raw) => {
        const opt = normalizeOption(raw);
        const isSelected = selected === opt.value;
        const isDefault = defaultValue === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => onChange(opt.value)}
            className="flex items-center gap-2 w-full text-left group"
          >
            <span className={`w-3.5 h-3.5 rounded-full border-2 flex items-center justify-center shrink-0 transition-colors ${
              isSelected ? "border-primary bg-primary" : "border-border-subtle group-hover:border-primary/60"
            }`}>
              {isSelected && <span className="w-1.5 h-1.5 rounded-full bg-white" />}
            </span>
            <span className={`text-xs flex-1 ${isSelected ? "text-fg" : "text-fg-secondary"}`}>
              {opt.label}
            </span>
            {isDefault && !isSelected && (
              <span className="text-[9px] px-1 py-0.5 rounded bg-primary/10 text-primary">Suggested</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

interface CompactCheckboxOptionsProps {
  options: Question["options"];
  selected: string[];
  defaultValue?: string;
  onChange: (v: string[]) => void;
}

function CompactCheckboxOptions({ options = [], selected, defaultValue, onChange }: CompactCheckboxOptionsProps) {
  return (
    <div className="space-y-1.5">
      {options.map((raw) => {
        const opt = normalizeOption(raw);
        const isSelected = selected.includes(opt.value);
        const isDefault = defaultValue === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => {
              onChange(
                isSelected
                  ? selected.filter((v) => v !== opt.value)
                  : [...selected, opt.value],
              );
            }}
            className="flex items-center gap-2 w-full text-left group"
          >
            <span className={`w-3.5 h-3.5 rounded-sm border-2 flex items-center justify-center shrink-0 transition-colors ${
              isSelected ? "border-primary bg-primary" : "border-border-subtle group-hover:border-primary/60"
            }`}>
              {isSelected && <Check className="w-2.5 h-2.5 text-white" />}
            </span>
            <span className={`text-xs flex-1 ${isSelected ? "text-fg" : "text-fg-secondary"}`}>
              {opt.label}
            </span>
            {isDefault && !isSelected && (
              <span className="text-[9px] px-1 py-0.5 rounded bg-primary/10 text-primary">Suggested</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

interface CardOptionsProps {
  options: Question["options"];
  selected: string | string[];
  defaultValue?: string;
  multi: boolean;
  onChange: (v: string | string[]) => void;
}

function CardOptions({ options = [], selected, defaultValue, multi, onChange }: CardOptionsProps) {
  const selectedArr = Array.isArray(selected) ? selected : [selected];

  return (
    <div className="space-y-2">
      {options.map((raw) => {
        const opt = normalizeOption(raw);
        const isSelected = selectedArr.includes(opt.value);
        const isDefault = defaultValue === opt.value;

        const handleClick = () => {
          if (multi) {
            const arr = selectedArr.includes(opt.value)
              ? selectedArr.filter((v) => v !== opt.value)
              : [...selectedArr, opt.value];
            onChange(arr);
          } else {
            onChange(opt.value);
          }
        };

        return (
          <button
            key={opt.value}
            type="button"
            onClick={handleClick}
            className={`w-full text-left rounded-md border-2 px-3 py-2.5 transition-colors ${
              isSelected
                ? "border-primary bg-primary/5"
                : "border-border-subtle hover:border-primary/40"
            }`}
          >
            <div className="flex items-start justify-between gap-2">
              <span className={`text-xs font-medium ${isSelected ? "text-fg" : "text-fg-secondary"}`}>
                {opt.label}
              </span>
              <div className="flex items-center gap-1.5 shrink-0">
                {isDefault && !isSelected && (
                  <span className="text-[9px] px-1 py-0.5 rounded bg-primary/10 text-primary">
                    Suggested
                  </span>
                )}
                {multi ? (
                  <span className={`w-3.5 h-3.5 rounded-sm border-2 flex items-center justify-center transition-colors ${
                    isSelected ? "border-primary bg-primary" : "border-border-subtle"
                  }`}>
                    {isSelected && <Check className="w-2.5 h-2.5 text-white" />}
                  </span>
                ) : (
                  <span className={`w-3.5 h-3.5 rounded-full border-2 flex items-center justify-center transition-colors ${
                    isSelected ? "border-primary bg-primary" : "border-border-subtle"
                  }`}>
                    {isSelected && <span className="w-1.5 h-1.5 rounded-full bg-white" />}
                  </span>
                )}
              </div>
            </div>
            {opt.description && (
              <p className="text-[11px] text-fg-muted mt-1 leading-relaxed">{opt.description}</p>
            )}
          </button>
        );
      })}
    </div>
  );
}
