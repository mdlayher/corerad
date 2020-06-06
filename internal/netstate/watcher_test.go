package netstate

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestWatcherWatch(t *testing.T) {
	// Test invariants for subscribed interfaces and their bitmasks.
	const (
		ifi0     = "test0"
		changes0 = LinkAny

		ifi1     = "test1"
		changes1 = LinkUp | LinkDown
	)

	tests := []struct {
		name  string
		in    changeSet
		check func(t *testing.T, oneC, twoC <-chan Change)
	}{
		{
			name: "no changes",
			check: func(t *testing.T, oneC <-chan Change, twoC <-chan Change) {
				_, ok1 := <-oneC
				_, ok2 := <-twoC

				if ok1 || ok2 {
					t.Fatalf("expected both channels to be closed: (%v, %v)", ok1, ok2)
				}
			},
		},
		{
			name: "change one interface",
			in: changeSet{
				ifi0: []Change{LinkUp},
				// Not subscribed to these changes.
				ifi1: []Change{LinkLowerLayerDown, LinkNotPresent},
				// Not subscribed on this interface.
				"test999": []Change{LinkUp},
			},
			check: func(t *testing.T, oneC <-chan Change, twoC <-chan Change) {
				if diff := cmp.Diff(LinkUp, <-oneC); diff != "" {
					t.Fatalf("unexpected up on first link (-want +got):\n%s", diff)
				}

				if _, ok := <-twoC; ok {
					t.Fatal("expected second channel to be closed")
				}
			},
		},
		{
			name: "change both interfaces",
			in: changeSet{
				ifi0: []Change{LinkLowerLayerDown},
				ifi1: []Change{LinkUp},
			},
			check: func(t *testing.T, oneC <-chan Change, twoC <-chan Change) {
				if diff := cmp.Diff(LinkLowerLayerDown, <-oneC); diff != "" {
					t.Fatalf("unexpected lower layer down on first link (-want +got):\n%s", diff)
				}

				if diff := cmp.Diff(LinkUp, <-twoC); diff != "" {
					t.Fatalf("unexpected up on second link (-want +got):\n%s", diff)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWatcher()
			w.watch = func(_ context.Context, notify func(changeSet)) error {
				// One shot notify with any changes we specify.
				notify(tt.in)
				return nil
			}

			oneC := w.Subscribe(ifi0, changes0)
			twoC := w.Subscribe(ifi1, changes1)

			var wg sync.WaitGroup
			wg.Add(1)

			go func() {
				defer wg.Done()
				if err := w.Watch(context.Background()); err != nil {
					panicf("failed to watch: %v", err)
				}
			}()

			wg.Wait()
			tt.check(t, oneC, twoC)
		})
	}
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
