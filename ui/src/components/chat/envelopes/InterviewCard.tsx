import { useState, useRef, useEffect } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Envelope, Question } from "@/lib/types";
import type { EnvelopeResponder } from "./EnvelopeRenderer";

interface InterviewCardProps {
  envelope: Envelope;
  onRespond?: EnvelopeResponder;
  /** Increments each time the user sends a new message — triggers dismiss. */
  userMessageCount?: number;
}

export function InterviewCard({ envelope, onRespond: _onRespond, userMessageCount }: InterviewCardProps) {
  // If the backend already recorded a response, show confirmation immediately.
  const alreadyAnswered = envelope.prior_response != null;

  const [dismissed, setDismissed] = useState(false);
  const [submitted, _setSubmitted] = useState(alreadyAnswered);

  // Dismiss when the user sends a new chat message while the card is open.
  const mountCountRef = useRef(userMessageCount);
  useEffect(() => {
    if (userMessageCount !== undefined && mountCountRef.current !== undefined) {
      if (userMessageCount > mountCountRef.current && !submitted) {
        setDismissed(true);
      }
    }
  }, [userMessageCount, submitted]);

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
      {/* Header */}
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

      {/* Body — questions rendered here in later tasks */}
      <div className="px-3 py-3 space-y-4">
        {(envelope.questions ?? []).map((q, i) => (
          <QuestionStub key={i} question={q} />
        ))}
      </div>

      {/* Footer placeholder */}
      <div className="px-3 pb-3 flex justify-end">
        <Button
          size="sm"
          className="bg-primary hover:bg-primary-hover text-white text-xs px-4 py-1 h-7"
          disabled
        >
          Submit
        </Button>
      </div>
    </div>
  );
}

// Temporary stub — replaced in Task 5.
function QuestionStub({ question }: { question: Question }) {
  return (
    <div>
      <label className="block text-xs font-medium text-fg-secondary mb-1">{question.prompt}</label>
      <p className="text-xs text-fg-faint italic">({question.type})</p>
    </div>
  );
}
