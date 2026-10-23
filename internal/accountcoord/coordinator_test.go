package accountcoord

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func authorized(t *testing.T) *Coordinator {
	t.Helper()
	c := New()
	op, err := c.Begin(context.Background(), "check")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Finish(op, true, false, true); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSingleSlotAndGeneration(t *testing.T) {
	c := authorized(t)
	ref := c.View().Reference
	var winners atomic.Int32
	var wg sync.WaitGroup
	leases := make(chan *Lease, 32)
	for range 32 {
		wg.Go(func() {
			lease, err := c.Acquire(context.Background(), ref)
			if err == nil {
				winners.Add(1)
				leases <- lease
			} else if !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 || c.View().Active != 1 {
		t.Fatal(winners.Load(), c.View())
	}
	lease := <-leases
	for _, kind := range []string{"check", "refresh", "cancel"} {
		if _, err := c.Begin(context.Background(), kind); !errors.Is(err, ErrBusy) {
			t.Fatal(kind, err)
		}
	}
	lease.Release(true)
	lease.Release(false) // duplicate settlement cannot quarantine a later owner
	if c.View().State != "idle" {
		t.Fatal(c.View())
	}
	op, err := c.Begin(context.Background(), "refresh")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Finish(op, true, false, true); err != nil {
		t.Fatal(err)
	}
	if c.View().Reference != ref {
		t.Fatal("refresh changed chain reference")
	}
	if _, err = New().Acquire(context.Background(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatal("restart accepted grant", err)
	}
}

func TestLogoutWaitsForVerifiedRelease(t *testing.T) {
	c := authorized(t)
	ref := c.View().Reference
	lease, err := c.Acquire(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan string, 1)
	go func() {
		op, err := c.Begin(context.Background(), "logout")
		if err != nil {
			t.Error(err)
		}
		finished <- op
	}()
	<-lease.Context().Done()
	select {
	case <-finished:
		t.Fatal("auth started before cleanup")
	default:
	}
	if c.View().Active != 1 {
		t.Fatal("cancellation released slot")
	}
	if _, err = c.Acquire(context.Background(), ref); err == nil {
		t.Fatal("acquired during logout")
	}
	lease.Release(true)
	op := <-finished
	if err = c.Finish("wrong-operation", true, false, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err = c.Finish(op, false, false, true); err != nil {
		t.Fatal(err)
	}
	if c.View().Generation == ref.Generation || c.View().State != "unverified" {
		t.Fatal(c.View())
	}
	if _, err = c.Acquire(context.Background(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestFailedCleanupAndCanceledHandoff(t *testing.T) {
	for _, mode := range []string{"cleanup", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			c := authorized(t)
			lease, err := c.Acquire(context.Background(), c.View().Reference)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := c.Begin(ctx, "login"); result <- err }()
			<-lease.Context().Done()
			switch mode {
			case "cleanup":
				lease.Release(false)
			case "deadline":
				cancel()
			case "close":
				c.Close()
				lease.Release(true)
			}
			if err = <-result; err == nil {
				t.Fatal("unsafe handoff allowed")
			}
			lease.Release(true)
			if _, err = c.Acquire(context.Background(), c.View().Reference); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthOwnerAndFailures(t *testing.T) {
	c := New()
	if _, err := c.Acquire(context.Background(), c.View().Reference); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Begin(ctx, "check"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Begin(context.Background(), "arbitrary"); err == nil {
		t.Fatal("unknown operation")
	}
	op, err := c.Begin(context.Background(), "login")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Begin(context.Background(), "logout"); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if err = c.Finish(op, true, true, true); err != nil {
		t.Fatal(err)
	}
	if c.View().State != "authenticating" {
		t.Fatal(c.View())
	}
	if _, err = c.Acquire(context.Background(), c.View().Reference); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	op, err = c.Begin(context.Background(), "cancel")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Finish(op, false, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Begin(context.Background(), "check"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	c.Close()
	if c.View().State != "closed" {
		t.Fatal(c.View())
	}
	if err = c.Finish(op, true, false, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	c = authorized(t)
	c.Quarantine()
	if c.View().State != "blocked" {
		t.Fatal(c.View())
	}
	if _, err = c.Begin(context.Background(), "logout"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
