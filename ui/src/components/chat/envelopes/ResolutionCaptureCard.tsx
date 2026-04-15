import { CheckCircle, Lightbulb, MinusCircle } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ResponseStatus } from "@/lib/envelope-response";
import type { EnvelopeResponder } from "./EnvelopeRenderer";

interface ResolutionCaptureData {
  ticket_id?: string;
  issue_summary?: string;
  categories: string[];
}

interface ResolutionCaptureCardProps {
  data: ResolutionCaptureData;
  /** @deprecated string-formatted path; kept while adopters migrate. */
  onSendMessage?: (content: string) => void;
  /** Typed Phase-3 S5 response path. Fires on submit/skip. */
  onRespond?: EnvelopeResponder;
}

type CardState = "idle" | "submitted" | "skipped";

const TIME_OPTIONS = ["< 15 min", "15-30 min", "30-60 min", "1-2 hours", "2+ hours"];

export function ResolutionCaptureCard({
  data,
  onSendMessage,
  onRespond,
}: ResolutionCaptureCardProps) {
  const [cardState, setCardState] = useState<CardState>("idle");
  const [whatFixedIt, setWhatFixedIt] = useState("");
  const [category, setCategory] = useState("");
  const [timeSpent, setTimeSpent] = useState("");
  const [relatedKB, setRelatedKB] = useState("");
  const [createArticle, setCreateArticle] = useState(true);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!whatFixedIt.trim()) return;

    setCardState("submitted");
    setSubmitError(null);

    if (onSendMessage) {
      const ticketLabel = data.ticket_id || "this issue";
      const fixPreview =
        whatFixedIt.trim().length > 100
          ? whatFixedIt.trim().slice(0, 100) + "..."
          : whatFixedIt.trim();

      onSendMessage(
        `Resolution captured for ${ticketLabel}:\n` +
          `- Fix: ${fixPreview}\n` +
          `- Category: ${category || "none"}\n` +
          `- Time spent: ${timeSpent || "not specified"}\n` +
          `- Create KB article: ${createArticle ? "yes" : "no"}`,
      );
    }

    if (!onRespond) return;
    try {
      await onRespond({
        status: ResponseStatus.Submitted,
        data: {
          ticket_id: data.ticket_id,
          what_fixed_it: whatFixedIt.trim(),
          category: category || null,
          time_spent: timeSpent || null,
          related_kb: relatedKB
            ? relatedKB
                .split(",")
                .map((s) => s.trim())
                .filter(Boolean)
            : [],
          create_kb_article: createArticle,
        },
      });
    } catch (err) {
      setCardState("idle");
      setSubmitError(err instanceof Error ? err.message : "Failed to submit resolution");
    }
  };

  const handleSkip = async () => {
    setSubmitError(null);
    if (onRespond) {
      try {
        await onRespond({ status: ResponseStatus.Cancelled, data: {} });
      } catch (err) {
        setSubmitError(err instanceof Error ? err.message : "Failed to record skip");
        return;
      }
    }
    setCardState("skipped");
  };

  if (cardState === "submitted") {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-4">
        <div className="flex items-center gap-2">
          <CheckCircle className="h-4 w-4 text-success" />
          <span className="text-sm text-success">Resolution captured — thank you!</span>
        </div>
      </div>
    );
  }

  if (cardState === "skipped") {
    return (
      <div className="rounded-sm border border-border-subtle bg-bg-elevated/30 p-4">
        <div className="flex items-center gap-2">
          <MinusCircle className="h-4 w-4 text-fg-muted" />
          <span className="text-sm text-fg-muted">Resolution capture skipped</span>
        </div>
      </div>
    );
  }

  const inputCls =
    "w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-primary placeholder:text-fg-faint";

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      {/* Header */}
      <div className="mb-1 flex items-center gap-2">
        <Lightbulb className="h-4 w-4 text-fg-secondary" />
        <h4 className="text-sm font-medium text-fg">Capture Resolution</h4>
      </div>
      <p className="mb-4 text-xs text-fg-muted">
        Help us improve the knowledge base — document what resolved this issue.
      </p>

      {/* Issue summary context */}
      {data.issue_summary && (
        <div className="mb-3 rounded-md border border-border-subtle/50 bg-surface/50 px-3 py-2">
          <span className="text-xs text-fg-muted">Issue: </span>
          <span className="text-xs text-fg-secondary">{data.issue_summary}</span>
        </div>
      )}

      {submitError && (
        <p className="mb-3 text-xs text-danger" role="alert">
          {submitError}
        </p>
      )}

      <form onSubmit={(e) => void handleSubmit(e)} className="space-y-3">
        {/* What Fixed It */}
        <div>
          <label className="mb-1 block text-xs font-medium text-fg-secondary">
            What Fixed It <span className="text-danger">*</span>
          </label>
          <textarea
            className={`${inputCls} min-h-[80px] resize-y`}
            value={whatFixedIt}
            onChange={(e) => setWhatFixedIt(e.target.value)}
            placeholder="Describe the resolution steps"
            rows={3}
            required
          />
        </div>

        {/* Issue Category */}
        {data.categories.length > 0 && (
          <div>
            <label className="mb-1 block text-xs font-medium text-fg-secondary">
              Issue Category
            </label>
            <select
              className={inputCls}
              value={category}
              onChange={(e) => setCategory(e.target.value)}
            >
              <option value="">Select category...</option>
              {data.categories.map((cat) => (
                <option key={cat} value={cat}>
                  {cat.charAt(0).toUpperCase() + cat.slice(1)}
                </option>
              ))}
            </select>
          </div>
        )}

        {/* Time Spent */}
        <div>
          <label className="mb-1 block text-xs font-medium text-fg-secondary">Time Spent</label>
          <select
            className={inputCls}
            value={timeSpent}
            onChange={(e) => setTimeSpent(e.target.value)}
          >
            <option value="">Select time range...</option>
            {TIME_OPTIONS.map((opt) => (
              <option key={opt} value={opt}>
                {opt}
              </option>
            ))}
          </select>
        </div>

        {/* Related KB Articles */}
        <div>
          <label className="mb-1 block text-xs font-medium text-fg-secondary">
            Related KB Articles
          </label>
          <input
            type="text"
            className={inputCls}
            value={relatedKB}
            onChange={(e) => setRelatedKB(e.target.value)}
            placeholder="Comma-separated KB IDs (optional)"
          />
        </div>

        {/* Create KB Article checkbox */}
        <div>
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={createArticle}
              onChange={(e) => setCreateArticle(e.target.checked)}
              className="accent-accent"
            />
            <span className="text-xs text-fg-secondary">Should this become a new KB article?</span>
          </label>
        </div>

        {/* Buttons */}
        <div className="flex items-center gap-2">
          <Button
            type="submit"
            size="sm"
            className="bg-primary hover:bg-primary-hover text-white text-xs px-4 py-1 h-8"
            disabled={!whatFixedIt.trim()}
          >
            Submit Resolution
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="text-xs text-fg-secondary hover:text-fg-secondary px-4 py-1 h-8"
            onClick={() => void handleSkip()}
          >
            Skip
          </Button>
        </div>
      </form>
    </div>
  );
}
