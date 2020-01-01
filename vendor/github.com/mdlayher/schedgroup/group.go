package schedgroup

import (
	"container/heap"
	"context"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// A Group is a goroutine worker pool which schedules tasks to be performed
// after a specified time. A Group must be created with the New constructor.
type Group struct {
	// Interval controls how often the Group will check for scheduled tasks
	// to run. A lower Interval will schedule tasks more accurately, but will
	// consume more CPU cycles.
	//
	// By default, Interval is 1 millisecond.
	Interval time.Duration

	// Context/cancelation support.
	ctx    context.Context
	cancel func()

	// Task runner and a heap of tasks to be run.
	eg    *errgroup.Group
	mu    sync.Mutex
	tasks tasks
}

// New creates a new Group which will use ctx for cancelation. If cancelation
// is not a concern, use context.Background().
func New(ctx context.Context) *Group {
	// Monitor goroutine context and cancelation.
	mctx, cancel := context.WithCancel(ctx)

	g := &Group{
		Interval: 1 * time.Millisecond,

		ctx:    ctx,
		cancel: cancel,

		eg: &errgroup.Group{},
	}

	g.eg.Go(func() error {
		return g.monitor(mctx)
	})

	return g
}

// Delay schedules a function to run at or after the specified delay. Delay
// is a convenience wrapper for Schedule which adds delay to the current time.
func (g *Group) Delay(delay time.Duration, fn func() error) {
	g.Schedule(time.Now().Add(delay), fn)
}

// Schedule schedules a function to run at or after the specified time.
func (g *Group) Schedule(when time.Time, fn func() error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	heap.Push(&g.tasks, task{
		Deadline: when,
		Call:     fn,
	})
}

// Wait waits for the completion of all scheduled tasks, or for cancelation of
// the context passed to New.
func (g *Group) Wait() error {
	t := time.NewTicker(g.Interval)

	// Tick and wait repeatedly to see if the monitor goroutine has consumed
	// and processed all of the available work.
	for {
		select {
		case <-g.ctx.Done():
			return g.ctx.Err()
		case <-t.C:
			// Context cancelation takes priority over new ticks.
			if err := g.ctx.Err(); err != nil {
				return err
			}
		}

		g.mu.Lock()
		if len(g.tasks) == 0 {
			// No more tasks left, cancel the monitor goroutine.
			defer g.mu.Unlock()
			g.cancel()
			break
		}
		g.mu.Unlock()
	}

	// Wait for all running tasks to complete.
	t.Stop()
	return g.eg.Wait()
}

// monitor triggers tasks at the interval specified by g.Interval until ctx
// is canceled.
func (g *Group) monitor(ctx context.Context) error {
	t := time.NewTicker(g.Interval)
	defer t.Stop()

	for {
		// monitor's cancelation is expected and should not result in an
		// error being returned to the caller.
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			// Context cancelation takes priority over new ticks.
			if ctx.Err() != nil {
				return nil
			}

			g.trigger(time.Now())
		}
	}
}

// trigger checks for scheduled tasks and runs them if they are scheduled
// on or after the time specified by now.
func (g *Group) trigger(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for g.tasks.Len() > 0 {
		next := &g.tasks[0]
		if next.Deadline.After(now) {
			// Earliest scheduled task is not ready.
			return
		}

		// This task is ready, pop it from the heap and run it.
		t := heap.Pop(&g.tasks).(task)
		g.eg.Go(t.Call)
	}
}

// A task is a function which is called after the specified deadline.
type task struct {
	Deadline time.Time
	Call     func() error
}

// tasks implements heap.Interface.
type tasks []task

var _ heap.Interface = &tasks{}

func (pq tasks) Len() int            { return len(pq) }
func (pq tasks) Less(i, j int) bool  { return pq[i].Deadline.Before(pq[j].Deadline) }
func (pq tasks) Swap(i, j int)       { pq[i], pq[j] = pq[j], pq[i] }
func (pq *tasks) Push(x interface{}) { *pq = append(*pq, x.(task)) }
func (pq *tasks) Pop() (item interface{}) {
	n := len(*pq)
	item, *pq = (*pq)[n-1], (*pq)[:n-1]
	return item
}
