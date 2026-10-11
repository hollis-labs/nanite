package service

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
)

// Execution capabilities (CW-20261011-0037). A capability is the host's
// in-process statement that THIS turn, of THIS native view, submitted by THIS
// credential, is executing now. It is not a string, header, session id, slug,
// definition pin, receipt or credential id, and nothing outside this package can
// build one: the type and its constructor are unexported, and a value is valid
// only while the registry holds that exact pointer.
//
// A Code Mode child gets its own capability (a narrowed subset, its own lifetime)
// that records both who spawned it (parentCapabilityID) and which view it may
// read history from (parentViewID). The two identifiers mean different things and
// are never interchangeable. A child's capability does not end when its parent's
// turn does.

var (
	ErrCapabilityEnded    = errors.New("execution capability has ended")
	ErrCapabilityNarrowed = errors.New("a child capability may only narrow its parent's tools")
	ErrCapabilityNested   = errors.New("a child capability cannot spawn another child")
)

// capabilityRegistry holds the live capabilities of the process.
type capabilityRegistry struct {
	mu   sync.Mutex
	live map[string]*executionCapability
}

func newCapabilityRegistry() *capabilityRegistry {
	return &capabilityRegistry{live: map[string]*executionCapability{}}
}

type executionCapability struct {
	id                 string
	subjectViewID      string // the view this capability executes for
	turnID, runID      string
	hostSettingsID     string // whose reviewed grants apply
	submittedBy        string // credential id; looked up on every dispatch (R1)
	parentCapabilityID string // provenance: which execution spawned it (child only)
	parentViewID       string // history/snapshot relationship (child only)
	narrowing          ToolNarrowing
	reg                *capabilityRegistry
}

// issueRoot is the only way a root capability comes into being. The chat
// service calls it when it starts a generation whose view has a native
// authority.
func (r *capabilityRegistry) issueRoot(subjectViewID, turnID, runID, hostSettingsID, submittedBy string, narrowing ToolNarrowing) (*executionCapability, error) {
	if subjectViewID == "" || turnID == "" || hostSettingsID == "" || submittedBy == "" {
		return nil, errors.New("a capability needs a view, turn, host settings and submitting credential")
	}
	c := &executionCapability{id: uuid.NewString(), subjectViewID: subjectViewID, turnID: turnID, runID: runID,
		hostSettingsID: hostSettingsID, submittedBy: submittedBy, narrowing: copyNarrowing(narrowing), reg: r}
	r.mu.Lock()
	r.live[c.id] = c
	r.mu.Unlock()
	return c, nil
}

// live reports whether this exact capability is registered.
func (c *executionCapability) isLive() bool {
	if c == nil || c.reg == nil {
		return false
	}
	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()
	return c.reg.live[c.id] == c
}

// end invalidates the capability. It does not touch capabilities it spawned.
func (c *executionCapability) end() {
	if c == nil || c.reg == nil {
		return
	}
	c.reg.mu.Lock()
	if c.reg.live[c.id] == c {
		delete(c.reg.live, c.id)
	}
	c.reg.mu.Unlock()
}

// child mints a NEW capability for a fork view. It records the parent's
// capability identity and the parent view as separate fields, may only narrow,
// inherits the submitting credential (so R1 applies to child dispatch) and
// gets its own lifetime. A child cannot itself spawn a child.
func (c *executionCapability) child(childViewID, turnID, runID string, narrowing ToolNarrowing) (*executionCapability, error) {
	if !c.isLive() {
		return nil, ErrCapabilityEnded
	}
	if c.parentViewID != "" {
		return nil, ErrCapabilityNested
	}
	if childViewID == "" || childViewID == c.subjectViewID || turnID == "" {
		return nil, errors.New("a child capability needs its own view and turn")
	}
	if narrowing == nil {
		return nil, ErrCapabilityNarrowed // a child must say what it keeps; it never silently inherits "everything"
	}
	if c.narrowing != nil {
		for tool, on := range narrowing {
			if on && !c.narrowing[tool] {
				return nil, ErrCapabilityNarrowed
			}
		}
	}
	k := &executionCapability{id: uuid.NewString(), subjectViewID: childViewID, turnID: turnID, runID: runID,
		hostSettingsID: c.hostSettingsID, submittedBy: c.submittedBy,
		parentCapabilityID: c.id, parentViewID: c.subjectViewID, narrowing: copyNarrowing(narrowing), reg: c.reg}
	c.reg.mu.Lock()
	c.reg.live[k.id] = k
	c.reg.mu.Unlock()
	return k, nil
}

func copyNarrowing(n ToolNarrowing) ToolNarrowing {
	if n == nil {
		return nil
	}
	out := make(ToolNarrowing, len(n))
	for k, v := range n {
		out[k] = v
	}
	return out
}

type executionCapabilityKey struct{}

func withExecutionCapability(ctx context.Context, c *executionCapability) context.Context {
	return context.WithValue(ctx, executionCapabilityKey{}, c)
}

// executionCapabilityFrom returns the capability in ctx only while it is live.
func executionCapabilityFrom(ctx context.Context) (*executionCapability, bool) {
	c, _ := ctx.Value(executionCapabilityKey{}).(*executionCapability)
	if !c.isLive() {
		return nil, false
	}
	return c, true
}

// AuthorizeCapability is the dispatch decision for a capability holder: the
// capability must be live, and the grants, the submitting credential and the
// capability's narrowing are all re-checked on this call.
func (g *NativeGrants) AuthorizeCapability(ctx context.Context, c *executionCapability, tool string) (bool, string) {
	if !c.isLive() {
		return false, "execution capability has ended"
	}
	return g.AuthorizeDispatch(ctx, NativeSubject{HostSettingsID: c.hostSettingsID, SubmittedBy: c.submittedBy}, c.narrowing, tool)
}
