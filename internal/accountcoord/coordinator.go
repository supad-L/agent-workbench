// Package accountcoord coordinates the single workbench-managed native auth
// namespace. It stores no credentials and does not identify provider quota domains.
package accountcoord

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
)

// ResourceID is a conservative shared slot, not a verified provider account ID.
const ResourceID = "codex-managed"

var (
	// ErrBusy means another auth operation or native execution owns this resource.
	ErrBusy = errors.New("账号正被认证操作或执行占用")
	// ErrUnavailable rejects stale grants, missing auth and unconfirmed cleanup.
	ErrUnavailable = errors.New("账号授权或执行清理条件尚未确认，请重新核对")
)

// Reference fences executions across account switches and backend restarts.
type Reference struct {
	ResourceID string `json:"resource_id"`
	Generation string `json:"generation"`
}

// View contains only coordination metadata; ready does not mean online readiness.
type View struct {
	Reference
	State         string `json:"state"`
	Active        int    `json:"active"`
	Limit         int    `json:"limit"`
	IdentityScope string `json:"identity_scope"`
	QuotaKnown    bool   `json:"quota_known"`
}

// Coordinator owns one auth chain and one native execution slot.
type Coordinator struct {
	mu                                          sync.Mutex
	reference                                   Reference
	operation                                   string
	authenticated, ownerActive, blocked, closed bool
	lease                                       *Lease
}

// New starts unverified, with a fresh generation. No auth is inferred on restart.
func New() *Coordinator {
	return &Coordinator{reference: Reference{ResourceID: ResourceID, Generation: rand.Text()}}
}

// View reads state without starting or refreshing an official process.
func (c *Coordinator) View() View {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := "unverified"
	switch {
	case c.closed:
		state = "closed"
	case c.blocked:
		state = "blocked"
	case c.operation != "":
		state = "authenticating"
	case c.lease != nil:
		state = "executing"
	case c.ownerActive:
		state = "authenticating"
	case c.authenticated:
		state = "idle"
	}
	active := 0
	if c.lease != nil {
		active = 1
	}
	return View{Reference: c.reference, State: state, Active: active, Limit: 1, IdentityScope: "managed_namespace"}
}

// Begin reserves the auth owner. Login/logout invalidate old grants and cancel
// execution, waiting for its verified cleanup before allowing any credential work.
// Check/refresh/cancel never interrupt an execution just to inspect credentials.
func (c *Coordinator) Begin(ctx context.Context, kind string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	revoke := kind == "login" || kind == "logout"
	if !revoke && kind != "check" && kind != "refresh" && kind != "cancel" {
		return "", errors.New("账号操作无效")
	}
	c.mu.Lock()
	if c.closed || c.blocked {
		c.mu.Unlock()
		return "", ErrUnavailable
	}
	if c.operation != "" || (c.lease != nil && !revoke) {
		c.mu.Unlock()
		return "", ErrBusy
	}
	op := rand.Text()
	c.operation = op
	if revoke {
		c.reference.Generation = rand.Text()
		c.authenticated = false
	}
	lease := c.lease
	if lease != nil {
		lease.cancel()
	}
	c.mu.Unlock()
	if lease != nil {
		select {
		case <-lease.done:
		case <-ctx.Done():
			c.mu.Lock()
			if c.operation == op {
				c.operation = ""
				c.authenticated = false
			}
			c.mu.Unlock()
			return "", ctx.Err()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		if c.operation == op {
			c.operation = ""
			c.authenticated = false
		}
		return "", err
	}
	if c.closed || c.blocked || c.operation != op {
		if c.operation == op {
			c.operation = ""
		}
		return "", ErrUnavailable
	}
	return op, nil
}

// Finish publishes only the matching auth operation. An owner still waiting for
// browser login retains the chain; failures block further handoff until restart.
func (c *Coordinator) Finish(operation string, authenticated, ownerActive, healthy bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || operation == "" || c.operation != operation {
		return ErrUnavailable
	}
	c.operation = ""
	c.authenticated = authenticated && healthy && !ownerActive
	c.ownerActive = ownerActive
	if !healthy {
		c.blocked = true
	}
	return nil
}

// Lease retains ownership through process shutdown and durable settlement.
type Lease struct {
	coordinator *Coordinator
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	once        sync.Once
}

// Acquire checks the fixed reference and never queues or starts an execution.
func (c *Coordinator) Acquire(ctx context.Context, reference Reference) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.blocked || reference != c.reference || !c.authenticated || c.ownerActive {
		return nil, ErrUnavailable
	}
	if c.operation != "" || c.lease != nil {
		return nil, ErrBusy
	}
	runCtx, cancel := context.WithCancel(ctx)
	lease := &Lease{coordinator: c, ctx: runCtx, cancel: cancel, done: make(chan struct{})}
	c.lease = lease
	return lease, nil
}

// Context is canceled on caller cancellation, logout, switching or shutdown.
func (l *Lease) Context() context.Context { return l.ctx }

// Release must follow cleanup and persistence. Unverified cleanup quarantines
// the resource instead of allowing an auth owner or another execution to overlap.
func (l *Lease) Release(verified bool) {
	l.once.Do(func() {
		c := l.coordinator
		c.mu.Lock()
		defer c.mu.Unlock()
		l.cancel()
		if !verified {
			c.blocked = true
			c.authenticated = false
		}
		if c.lease == l {
			c.lease = nil
		}
		close(l.done)
	})
}

// Quarantine preserves a durable unclean-run decision during startup reconciliation.
func (c *Coordinator) Quarantine() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = true
	c.authenticated = false
	if c.lease != nil {
		c.lease.cancel()
	}
}

// Close prevents new owners and cancels execution; its caller waits for runners.
func (c *Coordinator) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.authenticated = false
	if c.lease != nil {
		c.lease.cancel()
	}
}
