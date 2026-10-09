package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
)

func leaseFixture(ttl time.Duration) (capability.RuntimeIdentity, capability.GrantSet) {
	owner := capability.RuntimeIdentity{HostInstance: "lease-test", OwnerID: "reader", OwnerGeneration: 1}
	now := time.Now().UTC()
	grants := capability.GrantSet{{GrantID: "accepted-query", Name: capability.ReadonlyQuery, SchemaVersion: 1, Scope: json.RawMessage(`{"operations":["query"],"allowlists":{"sessions":["one"]}}`), HostInstance: owner.HostInstance, OwnerID: owner.OwnerID, OwnerGeneration: owner.OwnerGeneration, Audience: "nanite", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(ttl).Format(time.RFC3339Nano), PolicyRevision: "review-policy-v1"}}
	return owner, grants
}

func TestGrantLeaseExpiryCancelsPermitAndRefusesRevival(t *testing.T) {
	owner, grants := leaseFixture(35 * time.Millisecond)
	expired := make(chan struct{})
	lease, err := NewGrantLease(context.Background(), grants, owner, func() { close(expired) })
	if err != nil {
		t.Fatal(err)
	}
	permit, release, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	select {
	case <-permit.Done():
	case <-time.After(time.Second):
		t.Fatal("expiry did not cancel in-flight permit")
	}
	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("expiry did not revoke process privilege")
	}
	if _, _, err := lease.Acquire(context.Background()); !errors.Is(err, ErrGrantLeaseEnded) {
		t.Fatalf("expired dispatch: %v", err)
	}
	grants[0].ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if err := lease.Renew(context.Background(), grants); !errors.Is(err, ErrGrantLeaseEnded) {
		t.Fatalf("revival: %v", err)
	}
}

func TestGrantLeaseRenewalPreservesAuthorityAndRevocationWins(t *testing.T) {
	owner, grants := leaseFixture(time.Hour)
	lease, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Revoke()
	initial, release, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	next := copyGrants(grants)
	next[0].IssuedAt = time.Now().UTC().Format(time.RFC3339Nano)
	next[0].ExpiresAt = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	for _, mutate := range []func(*capability.Grant){func(g *capability.Grant) {
		g.Scope = json.RawMessage(`{"operations":["query"],"allowlists":{"sessions":["one","two"]}}`)
	}, func(g *capability.Grant) { g.PolicyRevision = "changed" }, func(g *capability.Grant) { g.OwnerGeneration++ }, func(g *capability.Grant) { g.GrantID = "different" }} {
		bad := copyGrants(next)
		mutate(&bad[0])
		if checkErr := lease.Renew(context.Background(), bad); checkErr == nil {
			t.Fatal("changed authority renewed")
		}
	}
	if checkErr := lease.Renew(context.Background(), next); checkErr != nil {
		t.Fatal(checkErr)
	}
	if initial.Err() == nil {
		t.Fatal("old permit retained after lease replacement")
	}
	current, done, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	lease.Revoke()
	if current.Err() == nil {
		t.Fatal("revocation did not cancel current permit")
	}
	if err := lease.Renew(context.Background(), next); !errors.Is(err, ErrGrantLeaseEnded) {
		t.Fatalf("revoked renewal: %v", err)
	}
}

func TestGrantLeaseRenewalDoesNotBindRequestLifetime(t *testing.T) {
	owner, grants := leaseFixture(time.Hour)
	host, stopHost := context.WithCancel(context.Background())
	defer stopHost()
	lease, err := NewGrantLease(host, grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Revoke()
	request, cancelRequest := context.WithCancel(context.Background())
	next := copyGrants(grants)
	next[0].ExpiresAt = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if checkErr := lease.Renew(request, next); checkErr != nil {
		t.Fatal(checkErr)
	}
	cancelRequest()
	permit, release, err := lease.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	stopHost()
	if permit.Err() == nil {
		t.Fatal("host shutdown did not cancel renewed permit")
	}
}

func TestGrantLeasePermitPreservesCallerDeadlineAndValues(t *testing.T) {
	owner, grants := leaseFixture(time.Hour)
	type callerKey struct{}
	lease, err := NewGrantLease(context.WithValue(context.Background(), callerKey{}, "host"), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Revoke()
	request, cancelRequest := context.WithTimeout(context.WithValue(context.Background(), callerKey{}, "caller"), time.Second)
	defer cancelRequest()
	permit, release, err := lease.Acquire(request)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	deadline, ok := permit.Deadline()
	want, _ := request.Deadline()
	if !ok || !deadline.Equal(want) || permit.Value(callerKey{}) != "caller" {
		t.Fatal("caller context lost")
	}
	cancelRequest()
	select {
	case <-permit.Done():
	case <-time.After(time.Second):
		t.Fatal("caller cancellation lost")
	}
	if _, err := lease.Context(); err != nil {
		t.Fatal("caller cancellation ended host lease")
	}
}

func TestGrantLeasePermitCannotOutliveLeaseDeadline(t *testing.T) {
	owner, grants := leaseFixture(time.Minute)
	lease, err := NewGrantLease(context.Background(), grants, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Revoke()
	caller, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	permit, release, err := lease.Acquire(caller)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	want, err := time.Parse(time.RFC3339Nano, grants[0].ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := permit.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatalf("forward deadline %v want lease expiry %v", got, want)
	}
}

func TestGrantLeaseHostLifetimeRevokesOwner(t *testing.T) {
	owner, grants := leaseFixture(time.Hour)
	host, stop := context.WithCancel(context.Background())
	ended := make(chan struct{})
	lease, err := NewGrantLease(host, grants, owner, func() { close(ended) })
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Revoke()
	stop()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("host shutdown did not revoke owner")
	}
	if _, _, err := lease.Acquire(context.Background()); !errors.Is(err, ErrGrantLeaseEnded) {
		t.Fatal("ended host dispatch", err)
	}
}
