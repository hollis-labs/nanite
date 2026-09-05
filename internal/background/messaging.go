package background

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
)

// envelopePostTimeout bounds the messaging.SendMessage call when
// posting the completion envelope. Independent of the job's wall-
// clock budget — the job may have run for half an hour, but pushing
// the result envelope through the messaging service should be near-
// instant; if it isn't, something is wrong with messaging itself,
// not the job.
const envelopePostTimeout = 30 * time.Second

// completionEnvelopeKind is the messaging.Kind for the completion
// notification. background_job results are not requests/replies in
// the messaging sense — they're agent-triggered notifications, same
// as the "report ready" pattern documented in messaging.go.
const completionEnvelopeKind = messaging.KindNotification

// completionEnvelopeChannel is the messaging.Channel for the
// completion notification. ChannelInbox so the originating session
// surfaces the result via inbox poll / notification rather than as
// inline chat (the originating turn has likely moved on).
const completionEnvelopeChannel = messaging.ChannelInbox

// completionSubject formats the completion envelope's subject line.
// Short, scan-friendly, includes the terminal status so the inbox
// list can render it without opening the body.
func completionSubject(status JobStatus) string {
	return "background_job " + string(status)
}

// completionBody is the human-readable body for the completion
// envelope. Kept short — the structured payload (with output,
// timestamps, etc.) rides in PayloadJSON for programmatic consumers.
func completionBody(result JobResult) string {
	switch result.Status {
	case StatusSucceeded:
		return "Background job " + result.JobID + " completed successfully."
	case StatusFailed:
		if result.Error != "" {
			return "Background job " + result.JobID + " failed: " + result.Error
		}
		return "Background job " + result.JobID + " failed."
	case StatusCanceled:
		return "Background job " + result.JobID + " was canceled."
	default:
		return "Background job " + result.JobID + " reached status " + string(result.Status) + "."
	}
}

// postCompletionEnvelope marshals the JobResult and posts it to the
// messenger as a completion notification. Errors are logged, not
// returned — the in-memory record already reflects the terminal
// state and the Backend has already cleaned up the process; a
// messaging failure here is observable in logs and via Status/Result
// polling.
func (svc *Service) postCompletionEnvelope(req JobRequest, result JobResult) {
	if svc.messenger == nil {
		slog.Warn("background: no messenger; completion envelope dropped",
			"job_id", result.JobID)
		return
	}
	payload, err := json.Marshal(result)
	if err != nil {
		slog.Error("background: marshal result payload",
			"job_id", result.JobID, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), envelopePostTimeout)
	defer cancel()

	_, err = svc.messenger.SendMessage(ctx, messaging.SendInput{
		FromSessionID: req.OriginatingSessionID,
		FromAgentID:   svc.senderAgentID,
		ToSessionID:   req.OriginatingSessionID,
		ToAgentID:     req.OriginatingAgentID,
		Channel:       completionEnvelopeChannel,
		Kind:          completionEnvelopeKind,
		Type:          messaging.TypeStatusUpdate,
		Subject:       completionSubject(result.Status),
		Body:          completionBody(result),
		PayloadJSON:   string(payload),
		// Auto-register the synthetic sender as 'external' so the
		// messaging Service's first-send hook accepts it. The slug is
		// stable across jobs (SenderAgentID), so this only fires once
		// per process lifetime.
		RegisterAs: "external",
	})
	if err != nil {
		slog.Error("background: post completion envelope",
			"job_id", result.JobID, "status", string(result.Status), "err", err)
	}
}
