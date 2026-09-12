package tetherbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	messaging "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/delivery"
	tether "github.com/hollis-labs/go-tether-client"
)

var testRecipient = messaging.Address{
	Kind: messaging.KindAgent, Authority: "nanite", ID: "agt_testactor",
}

// stubDaemon serves the three delivery routes. It exists because the two
// behaviors this file exists to encode — a CLAMPED lease and a DEAD-LETTERING
// nack — are things a healthy daemon will not produce on demand, so a live
// daemon could not exercise either.
//
// It drives the REAL client rather than a fake satisfying claimer. That is the
// point: a fake proves the wrapper's arithmetic, and only the real client
// proves the wire shapes it decodes. The client's own transport accepts an
// http:// base URL, which is what makes this possible without a socket.
type stubDaemon struct {
	grantedLeaseSeconds int
	nackStatus          delivery.DeliveryStatus
	deadLetterReason    string

	sawLease delivery.LeaseRef
	sawStage delivery.ReceiptStage
	sawNack  struct {
		Retryable bool
		Reason    string
	}
}

func (d *stubDaemon) server(t *testing.T) *tether.Client {
	t.Helper()
	lease := delivery.LeaseRef{
		DeliveryID: "dlv-1", AttemptID: "att-1", LeaseToken: "tok-1", BindingGeneration: 7,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/messages/msg-1/claim", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"message":       messaging.Envelope{ID: "msg-1"},
			"lease":         lease,
			"lease_seconds": d.grantedLeaseSeconds,
		})
	})
	mux.HandleFunc("/messages/msg-1/ack", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Lease delivery.LeaseRef     `json:"lease"`
			Stage delivery.ReceiptStage `json:"stage"`
		}
		decodeJSON(t, r, &body)
		d.sawLease, d.sawStage = body.Lease, body.Stage
		writeJSON(t, w, map[string]any{
			"delivery": delivery.RecipientDelivery{ID: "dlv-1", Status: delivery.DeliveryDelivered},
			"attempt":  delivery.Attempt{ID: "att-1", Stage: body.Stage},
		})
	})
	mux.HandleFunc("/messages/msg-1/nack", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Retryable bool   `json:"retryable"`
			Error     string `json:"error"`
		}
		decodeJSON(t, r, &body)
		d.sawNack.Retryable, d.sawNack.Reason = body.Retryable, body.Error
		writeJSON(t, w, map[string]any{
			"delivery": delivery.RecipientDelivery{
				ID: "dlv-1", Status: d.nackStatus, DeadLetterReason: d.deadLetterReason,
			},
			"attempt": delivery.Attempt{ID: "att-1"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := tether.New(srv.URL, tether.WithSelfURN(testRecipient.URN()))
	if err != nil {
		t.Fatalf("tether.New: %v", err)
	}
	return c
}

// TestClaimDelivery_HonorsTheGrantedLeaseNotTheRequest is behavior 1. The
// daemon clamps, and a caller that trusts its own request believes a lease is
// alive after it has expired.
func TestClaimDelivery_HonorsTheGrantedLeaseNotTheRequest(t *testing.T) {
	d := &stubDaemon{grantedLeaseSeconds: 30}
	c := d.server(t)

	claim, err := ClaimDelivery(context.Background(), c, "msg-1", testRecipient,
		ClaimOptions{LeaseSeconds: 3600}) // ask for an hour
	if err != nil {
		t.Fatalf("ClaimDelivery: %v", err)
	}
	if claim.GrantedFor != 30*time.Second {
		t.Errorf("GrantedFor = %v, want 30s — the daemon's grant, not the 3600s requested", claim.GrantedFor)
	}
	if claim.ExpiresAt.IsZero() {
		t.Error("ExpiresAt not derived from the grant")
	}
	// The deadline must follow the grant, so a caller cannot accidentally
	// reason from its own request.
	if !claim.Expired(time.Now().Add(31 * time.Second)) {
		t.Error("claim not expired 31s after a 30s grant")
	}
	if claim.Expired(time.Now().Add(29 * time.Second)) {
		t.Error("claim expired 29s into a 30s grant")
	}
	if claim.Lease.LeaseToken != "tok-1" || claim.Lease.BindingGeneration != 7 {
		t.Errorf("lease decoded wrong: %+v — this is the wire shape a fake could not catch", claim.Lease)
	}
}

// TestFailDelivery_NonRetryableDeadLettersDespiteNilError is behavior 2, and
// the one most likely to cause a silent bug: the client returns nil, so a
// caller checking only err retries something already dead-lettered and never
// finds out.
func TestFailDelivery_NonRetryableDeadLettersDespiteNilError(t *testing.T) {
	d := &stubDaemon{
		grantedLeaseSeconds: 60,
		nackStatus:          delivery.DeliveryDeadLettered,
		deadLetterReason:    "handler rejected the payload",
	}
	c := d.server(t)
	claim, err := ClaimDelivery(context.Background(), c, "msg-1", testRecipient, ClaimOptions{})
	if err != nil {
		t.Fatalf("ClaimDelivery: %v", err)
	}

	rd, err := FailDelivery(context.Background(), c, "msg-1", testRecipient, claim,
		NackOptions{Retryable: false, Reason: "handler rejected the payload"})
	if err == nil {
		t.Fatal("a dead-lettered delivery reported success; the caller would retry it forever " +
			"and nothing would error")
	}
	if !errors.Is(err, ErrDeadLettered) {
		t.Errorf("error = %v, want ErrDeadLettered", err)
	}
	if rd.Status != delivery.DeliveryDeadLettered {
		t.Errorf("Status = %q, want dead_lettered", rd.Status)
	}
	if !strings.Contains(err.Error(), "handler rejected the payload") {
		t.Errorf("error omits the dead-letter reason, which is what a trace would show: %v", err)
	}
	if d.sawNack.Retryable {
		t.Error("Retryable=true reached the daemon for a non-retryable failure")
	}
}

// TestFailDelivery_RetryableIsNotAnError keeps the two nack outcomes
// distinguishable. A scheduled retry is not a failure of the call.
func TestFailDelivery_RetryableIsNotAnError(t *testing.T) {
	d := &stubDaemon{grantedLeaseSeconds: 60, nackStatus: delivery.DeliveryRetryScheduled}
	c := d.server(t)
	claim, _ := ClaimDelivery(context.Background(), c, "msg-1", testRecipient, ClaimOptions{})

	rd, err := FailDelivery(context.Background(), c, "msg-1", testRecipient, claim,
		NackOptions{Retryable: true, Reason: "transient"})
	if err != nil {
		t.Fatalf("retryable nack returned an error: %v", err)
	}
	if rd.Status != delivery.DeliveryRetryScheduled {
		t.Errorf("Status = %q, want retry_scheduled", rd.Status)
	}
	if !d.sawNack.Retryable {
		t.Error("Retryable=false reached the daemon for a retryable failure")
	}
}

// TestAcceptDelivery_PassesTheLeaseBackUnmodified — the daemon checks the lease
// belongs to the message in the path, so mutating it turns a correct cycle into
// a rejected one.
func TestAcceptDelivery_PassesTheLeaseBackUnmodified(t *testing.T) {
	d := &stubDaemon{grantedLeaseSeconds: 60}
	c := d.server(t)
	claim, err := ClaimDelivery(context.Background(), c, "msg-1", testRecipient, ClaimOptions{})
	if err != nil {
		t.Fatalf("ClaimDelivery: %v", err)
	}

	if _, err := AcceptDelivery(context.Background(), c, "msg-1", testRecipient, claim); err != nil {
		t.Fatalf("AcceptDelivery: %v", err)
	}
	if d.sawLease != claim.Lease {
		t.Errorf("lease reached the daemon as %+v, want the claimed %+v", d.sawLease, claim.Lease)
	}
	if d.sawStage != delivery.StageHostAccepted {
		t.Errorf("stage = %q, want host_accepted", d.sawStage)
	}
}

// TestCycleStagesAreDistinct pins that the three acks report different stages.
// Collapsing them would report work as consumed the moment it was accepted,
// which is the overclaim the architecture's staged receipts exist to prevent.
func TestCycleStagesAreDistinct(t *testing.T) {
	d := &stubDaemon{grantedLeaseSeconds: 60}
	c := d.server(t)
	claim, _ := ClaimDelivery(context.Background(), c, "msg-1", testRecipient, ClaimOptions{})

	for _, tc := range []struct {
		name string
		call func() (delivery.RecipientDelivery, error)
		want delivery.ReceiptStage
	}{
		{"accept", func() (delivery.RecipientDelivery, error) {
			return AcceptDelivery(context.Background(), c, "msg-1", testRecipient, claim)
		}, delivery.StageHostAccepted},
		{"submit", func() (delivery.RecipientDelivery, error) {
			return SubmitDelivery(context.Background(), c, "msg-1", testRecipient, claim)
		}, delivery.StageTurnSubmitted},
		{"consume", func() (delivery.RecipientDelivery, error) {
			return ConsumeDelivery(context.Background(), c, "msg-1", testRecipient, claim)
		}, delivery.StageConsumed},
	} {
		if _, err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if d.sawStage != tc.want {
			t.Errorf("%s reported stage %q, want %q", tc.name, d.sawStage, tc.want)
		}
	}
}

func TestCycleRejectsNilClaimer(t *testing.T) {
	ctx := context.Background()
	if _, err := ClaimDelivery(ctx, nil, "msg-1", testRecipient, ClaimOptions{}); err == nil {
		t.Error("ClaimDelivery accepted a nil claimer")
	}
	if _, err := AcceptDelivery(ctx, nil, "msg-1", testRecipient, Claim{}); err == nil {
		t.Error("AcceptDelivery accepted a nil claimer")
	}
	if _, err := FailDelivery(ctx, nil, "msg-1", testRecipient, Claim{}, NackOptions{}); err == nil {
		t.Error("FailDelivery accepted a nil claimer")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode stub response: %v", err)
	}
}

func decodeJSON(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Errorf("decode stub request: %v", err)
	}
}
