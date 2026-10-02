import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import type { PluginInstallReview, PluginInstallReviewResponse } from "@/lib/types";

function declarations(review: PluginInstallReview): Map<string, string> {
  return new Map([
    ...review.capabilities.map((cap) => [`Capability: ${cap.name}`, `${cap.reason}${cap.optional ? " (optional)" : ""}`] as const),
    ...review.secrets.map((secret) => [`Secret: ${secret.name}`, `${secret.environment || "Plugin keychain"}${secret.required ? " (required)" : " (optional)"}`] as const),
    ...review.environment.map((name) => [`Configuration environment: ${name}`, name] as const),
    ...review.tools.map((tool) => [`Tool: ${tool.name}`, tool.effect] as const),
  ]);
}

export function PluginReviewDialog({ review, busy, onCancel, onApprove }: {
  review: PluginInstallReviewResponse | null;
  busy: boolean;
  onCancel: () => void;
  onApprove: () => void;
}) {
  if (!review) return null;
  const current = declarations(review.review);
  const previous = review.previous ? declarations(review.previous.review) : new Map<string, string>();
  const names = Array.from(new Set([...current.keys(), ...previous.keys()])).sort();
  return (
    <AlertDialog open onOpenChange={(open) => { if (!open && !busy) onCancel(); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Review {review.review.name}</AlertDialogTitle>
          <AlertDialogDescription>
            {review.review.id} · v{review.review.version}. Approve the capabilities and secrets declared by this bundle before it runs.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="max-h-[55vh] overflow-y-auto space-y-3 text-sm">
          {review.previous && <p className="text-fg-muted">Updating v{review.previous.review.version} → v{review.review.version}.</p>}
          <p>Executable: <code>{review.review.entrypoint} {review.review.arguments.join(" ")}</code></p>
          {names.length === 0 && <p>No capabilities, secrets, or tools declared.</p>}
          {names.map((name) => {
            const before = previous.get(name);
            const after = current.get(name);
            const changed = review.previous !== null && before !== after;
            return <div key={name} className="rounded-md border p-2">
              <p className="font-medium">{name}{changed ? (before === undefined ? " · Added" : after === undefined ? " · Removed" : " · Changed") : ""}</p>
              {changed && before !== undefined && <p className="text-fg-muted">Before: {before}</p>}
              {after !== undefined && <p>{changed ? "After: " : ""}{after}</p>}
            </div>;
          })}
          <p className="text-xs break-all text-fg-muted">Bundle: {review.review.bundle_digest}</p>
          {review.previous && review.previous.review.bundle_digest !== review.review.bundle_digest && <p className="text-xs text-fg-muted">The bundle contents changed. This approval applies to the reviewed copy.</p>}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy} onClick={onCancel}>Cancel</AlertDialogCancel>
          <AlertDialogAction disabled={busy} onClick={onApprove}>Approve and install</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
