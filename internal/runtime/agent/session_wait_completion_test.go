package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/acp"
	runtimeevents "github.com/hollis-labs/substrate/harness/adapters/runtimeevents"
)

func TestSessionWait_WaitsForInternalRetirement(t *testing.T) {
	for _, successorBound := range []bool{false, true} {
		name := "terminal_binding"
		if successorBound {
			name = "successor_binding"
		}
		t.Run(name, func(t *testing.T) {
			client := newBootACPTestClient()
			deps, _ := acpBootTestDeps(t, client)
			tailEntered := make(chan struct{})
			tailRelease := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(tailRelease) }) }
			t.Cleanup(release)
			deps.RuntimeEventSink = func(string, bool) runtimeevents.Sink {
				return runtimeevents.SinkFunc(func(ctx context.Context, ev runtimeevents.Event) error {
					if ev.Kind != runtimeevents.KindProcessExited {
						return nil
					}
					close(tailEntered)
					select {
					case <-tailRelease:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}
			sess, err := Boot(context.Background(), deps, Options{
				Mode: ModeLongLived, SessionID: "wait-retirement", Workdir: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("Boot: %v", err)
			}
			client.failProcess("child exited 42")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			select {
			case <-tailEntered:
			case <-ctx.Done():
				t.Fatal("process-exit sink did not enter the tail barrier")
			}
			var successor *Session
			if successorBound {
				successor = &Session{ID: sess.ID}
				if old, ok, swapErr := deps.Manager.Swap(sess.ID, successor); swapErr != nil || !ok || old != sess {
					t.Fatalf("Swap predecessor = %p, %v, %v; want %p", old, ok, swapErr, sess)
				}
			}

			// The sink barrier lets us acquire the retirement lock before Run
			// finishes. runDone must still close under this lock: Shutdown owns
			// the binding until that terminal boundary, while Wait owns the tail.
			func() {
				deps.Manager.mu.Lock()
				defer deps.Manager.mu.Unlock()
				release()
				select {
				case <-sess.runDone:
				case <-ctx.Done():
					t.Fatal("terminal notification waited for binding retirement")
				}
				waitCtx, waitCancel := context.WithTimeout(ctx, 25*time.Millisecond)
				defer waitCancel()
				if err := sess.Wait(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Wait before retirement = %v, want deadline exceeded", err)
				}
			}()

			// Every waiter must observe the original terminal error only after
			// exact-generation retirement completes, including a replaced owner.
			waiters := make(chan error, 8)
			for range cap(waiters) {
				go func() { waiters <- sess.Wait(ctx) }()
			}
			for range cap(waiters) {
				err := <-waiters
				var terminal *acp.LifecycleError
				if !errors.As(err, &terminal) || terminal.Kind != acp.OutcomeChildExit {
					t.Fatalf("Wait = %v, want child-exit lifecycle error", err)
				}
				got, ok := deps.Manager.Load(sess.ID)
				if successorBound {
					if !ok || got != successor {
						t.Fatalf("retirement changed successor binding: %p, %v", got, ok)
					}
				} else if ok {
					t.Fatal("Wait returned while the terminal binding remained registered")
				}
			}
		})
	}
}

func TestSessionWait_ExternalObserverRetainsRecoveryCustody(t *testing.T) {
	client := newBootACPTestClient()
	deps, _ := acpBootTestDeps(t, client)
	sess, err := Boot(context.Background(), deps, Options{
		Mode: ModeLongLived, SessionID: "wait-external-owner", Workdir: t.TempDir(),
		ExternalLifecycleObserver: true,
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	client.failProcess("child exited 42")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var terminal *acp.LifecycleError
	if err := sess.Wait(ctx); !errors.As(err, &terminal) || terminal.Kind != acp.OutcomeChildExit {
		t.Fatalf("Wait = %v, want child-exit lifecycle error", err)
	}
	if got, ok := deps.Manager.Load(sess.ID); !ok || got != sess {
		t.Fatalf("Wait retired external observer's binding: %p, %v", got, ok)
	}
	lease, ok := deps.Manager.BeginRecovery(sess.ID, sess)
	if !ok {
		t.Fatal("external observer could not claim exact terminal generation after Wait")
	}
	deps.Manager.EndRecovery(sess.ID, lease)
}
