import { useState } from "react";
import { Button } from "@/components/ui/button";
import { type Answer, ResponseStatus } from "@/lib/envelope-response";
import type { Question } from "@/lib/types";
import type { EnvelopeResponder } from "./EnvelopeRenderer";

// Options can be strings or {value, label} objects — normalize to {value, label}.
function normalizeOption(opt: unknown): { value: string; label: string } {
  if (typeof opt === "string") return { value: opt, label: opt };
  if (opt && typeof opt === "object" && "value" in opt) {
    const o = opt as { value: string; label?: string };
    return { value: o.value, label: o.label || o.value };
  }
  return { value: String(opt), label: String(opt) };
}

interface QuestionFormProps {
  questions: Question[];
  /** Legacy free-text submission path — still supported for un-migrated hosts. */
  onSubmit?: (formatted: string) => void;
  /** Typed Phase-3 S5 response path. When provided, fires alongside onSubmit. */
  onRespond?: EnvelopeResponder;
}

/**
 * Questions are position-indexed upstream; use `q-{idx}` as the stable id so
 * a reviewer can correlate `answers[].questionId` back to the prompt array.
 */
function questionId(idx: number): string {
  return `q-${idx}`;
}

export function QuestionForm({ questions, onSubmit, onRespond }: QuestionFormProps) {
  const [answers, setAnswers] = useState<Record<number, string | string[]>>(() => {
    const initial: Record<number, string | string[]> = {};
    questions.forEach((q, i) => {
      if (q.type === "checkbox") {
        initial[i] = q.default ? [q.default] : [];
      } else {
        initial[i] = q.default || "";
      }
    });
    return initial;
  });
  const [submitted, setSubmitted] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const handleSubmit = async () => {
    const lines = questions.map((q, i) => {
      const answer = answers[i];
      const answerStr = Array.isArray(answer) ? answer.join(", ") : answer;
      return `- ${q.prompt}: ${answerStr}`;
    });
    const formatted = `Answers:\n${lines.join("\n")}`;
    onSubmit?.(formatted);

    const typed: Answer[] = questions.map((_q, i) => ({
      questionId: questionId(i),
      value: answers[i] ?? "",
    }));
    setSubmitted(true);
    setSubmitError(null);
    if (!onRespond) return;
    try {
      await onRespond({ status: ResponseStatus.Submitted, answers: typed });
    } catch (err) {
      setSubmitted(false);
      setSubmitError(err instanceof Error ? err.message : "Failed to submit answers");
    }
  };

  if (submitted) {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-4">
        <p className="text-sm text-success">Answers submitted</p>
      </div>
    );
  }

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4 space-y-4">
      {questions.map((q, i) => (
        <div key={i}>
          <label className="block text-sm font-medium text-fg-secondary mb-1.5">
            {q.prompt}
            {q.required && <span className="text-danger ml-0.5">*</span>}
          </label>

          {q.type === "textarea" && (
            <textarea
              value={String(answers[i] ?? "")}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              rows={3}
              className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-primary resize-none"
            />
          )}

          {q.type === "text" && (
            <input
              type="text"
              value={String(answers[i] ?? "")}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-primary"
            />
          )}

          {q.type === "select" && q.options && (
            <select
              value={String(answers[i] ?? "")}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-primary"
            >
              <option value="">Select...</option>
              {q.options.map((raw) => {
                const opt = normalizeOption(raw);
                return (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                );
              })}
            </select>
          )}

          {q.type === "radio" && q.options && (
            <div className="space-y-1.5">
              {q.options.map((raw) => {
                const opt = normalizeOption(raw);
                return (
                  <label
                    key={opt.value}
                    className="flex items-center gap-2 text-sm text-fg-secondary cursor-pointer"
                  >
                    <input
                      type="radio"
                      name={`question-${i}`}
                      value={opt.value}
                      checked={answers[i] === opt.value}
                      onChange={() => setAnswers((a) => ({ ...a, [i]: opt.value }))}
                      className="accent-accent"
                    />
                    {opt.label}
                  </label>
                );
              })}
            </div>
          )}

          {q.type === "checkbox" && q.options && (
            <div className="space-y-1.5">
              {q.options.map((raw) => {
                const opt = normalizeOption(raw);
                const selected = Array.isArray(answers[i]) ? (answers[i] as string[]) : [];
                return (
                  <label
                    key={opt.value}
                    className="flex items-center gap-2 text-sm text-fg-secondary cursor-pointer"
                  >
                    <input
                      type="checkbox"
                      value={opt.value}
                      checked={selected.includes(opt.value)}
                      onChange={(e) => {
                        setAnswers((a) => {
                          const current = Array.isArray(a[i]) ? [...(a[i] as string[])] : [];
                          if (e.target.checked) {
                            return { ...a, [i]: [...current, opt.value] };
                          } else {
                            return { ...a, [i]: current.filter((v) => v !== opt.value) };
                          }
                        });
                      }}
                      className="accent-accent"
                    />
                    {opt.label}
                  </label>
                );
              })}
            </div>
          )}
        </div>
      ))}

      {submitError && (
        <p className="text-xs text-danger" role="alert">
          {submitError}
        </p>
      )}

      <Button
        size="sm"
        className="bg-primary hover:bg-primary-hover text-white text-xs px-4 py-1 h-7"
        onClick={() => void handleSubmit()}
      >
        Submit Answers
      </Button>
    </div>
  );
}
