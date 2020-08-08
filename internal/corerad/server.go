// Copyright 2019 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package corerad

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/netstate"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/sdnotify"
	"golang.org/x/sync/errgroup"
)

// A Server coordinates the goroutines that handle various pieces of the
// CoreRAD server.
type Server struct {
	cctx *Context
	t    *terminator
	w    *netstate.Watcher
}

// NewServer creates a Server with the input configuration and logger. If ll
// is nil, logs are discarded.
func NewServer(cctx *Context) *Server {
	return &Server{
		cctx: cctx,
		t:    &terminator{},
		w:    netstate.NewWatcher(),
	}
}

// A Task is a Server-owned task which will run until the input context is
// canceled.
type Task interface {
	// Run runs the Task until ctx is canceled or an error occurs. If ctx is
	// canceled, the Task should consume that error internally and return nil.
	Run(ctx context.Context) error

	// Ready indicates if a Task has been fully initialized once.
	Ready() <-chan struct{}

	// String returns information about a Task.
	fmt.Stringer
}

// BuildTasks produces Tasks for the Server to run from the input configuration
// and termination check function. terminate reports whether the process should
// expect to be terminated and stopped or immediately reloaded by a supervision
// daemon.
func (s *Server) BuildTasks(cfg config.Config, debug http.Handler) []Task {
	// Serve on each specified interface.
	var tasks []Task
	for _, ifi := range cfg.Interfaces {
		if !ifi.Advertise && !ifi.Monitor {
			s.cctx.ll.Printf("%s: interface is not advertising or monitoring, skipping initialization", ifi.Name)
			continue
		}

		// Register interest for link down events so this interface's Advertiser
		// can react accordingly.
		//
		// TODO: more events? It seems that rtnetlink at least generates a
		// variety of events when a link is brought up and we don't want the
		// Advertiser to flap.
		var watchC <-chan netstate.Change
		if s.w != nil {
			watchC = s.w.Subscribe(ifi.Name, netstate.LinkDown)
		}

		switch {
		case ifi.Advertise:
			dialer := system.NewDialer(ifi.Name, s.cctx.state, system.Advertise, s.cctx.ll)

			tasks = append(
				tasks,
				NewAdvertiser(s.cctx, ifi, dialer, watchC, s.t.terminate),
			)
		case ifi.Monitor:
			dialer := system.NewDialer(ifi.Name, s.cctx.state, system.Monitor, s.cctx.ll)

			tasks = append(
				tasks,
				NewMonitor(s.cctx, ifi.Name, dialer, watchC, ifi.Verbose),
			)
		default:
			panicf("corerad: Server interface %q is not advertising or monitoring", ifi.Name)
		}
	}

	// Optionally configure the debug HTTP server task.
	if d := cfg.Debug; d.Address != "" {
		s.cctx.ll.Printf("starting HTTP debug listener on %q: prometheus: %t, pprof: %t",
			d.Address, d.Prometheus, d.PProf)

		tasks = append(tasks, &httpTask{
			addr:   d.Address,
			h:      debug,
			ll:     s.cctx.ll,
			readyC: make(chan struct{}),
		})
	}

	// Optionally configure the link state watcher task.
	if s.w != nil {
		tasks = append(tasks, &watcherTask{
			watch: s.w.Watch,
			ll:    s.cctx.ll,
		})
	}

	return tasks
}

// Serve starts the CoreRAD server and runs Tasks until a signal is received,
// indicating a shutdown.
func (s *Server) Serve(sigC chan os.Signal, n *sdnotify.Notifier, tasks []Task) error {
	// Attach a context to the errgroup so that goroutines are canceled when one
	// of them returns an error.
	//
	// The inner context provides cancelation support for the signal watcher,
	// which is also implemented as an additional Task to control the lifetime
	// of the Serve function as a whole.
	eg, ctx := errgroup.WithContext(context.Background())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	tasks = append(tasks, &signalTask{
		sigC:   sigC,
		cancel: cancel,
		ll:     s.cctx.ll,
		n:      n,
		t:      s.t,
	})

	var wg sync.WaitGroup
	wg.Add(len(tasks))

	for _, t := range tasks {
		// Capture range variable for goroutines.
		t := t
		eg.Go(func() error {
			if err := t.Run(ctx); err != nil {
				return fmt.Errorf("failed to run task %s: %v", t, err)
			}

			return nil
		})

		go func() {
			defer wg.Done()
			<-t.Ready()
			_ = n.Notify(sdnotify.Statusf("started %s", t))
		}()
	}

	go func() {
		wg.Wait()
		_ = n.Notify(sdnotify.Statusf("server started, all tasks running"), sdnotify.Ready)
	}()

	// Wait for all goroutines to be canceled and stopped successfully.
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}

	return nil
}

// An httpTask is a Task which serves a debug HTTP server.
type httpTask struct {
	addr   string
	h      http.Handler
	ll     *log.Logger
	readyC chan struct{}
}

// Run implements Task.
func (t *httpTask) Run(ctx context.Context) error {
	// Wait 3 seconds between listen attempts.
	return serve(ctx, t.ll, 3*time.Second, func() error {
		l, err := net.Listen("tcp", t.addr)
		if err != nil {
			return err
		}

		// Listener ready, wait for cancelation via context and serve
		// the HTTP server until context is canceled, then immediately
		// close the server.
		var wg sync.WaitGroup
		wg.Add(1)
		defer wg.Wait()

		s := &http.Server{
			ReadTimeout: 1 * time.Second,
			Handler:     t.h,
			ErrorLog:    t.ll,
		}

		go func() {
			defer wg.Done()
			<-ctx.Done()
			_ = s.Close()
		}()

		close(t.readyC)
		return s.Serve(l)
	})
}

// Ready implements Task.
func (t *httpTask) Ready() <-chan struct{} { return t.readyC }

// String implements Task.
func (t *httpTask) String() string {
	return fmt.Sprintf("debug HTTP server %q", t.addr)
}

// serve invokes fn with retries until a listener is started, handling certain
// network listener errors as appropriate.
func serve(ctx context.Context, ll *log.Logger, delay time.Duration, fn func() error) error {
	const attempts = 40

	var nerr *net.OpError
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return nil
		}

		// Don't wait on the first attempt.
		if i != 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
			}
		}

		err := fn()
		switch {
		case errors.Is(err, http.ErrServerClosed):
			// Expected shutdown.
			return nil
		case errors.As(err, &nerr):
			// Handle outside switch.
		case err == nil:
			panic("corerad: serve function should never return nil")
		default:
			// Nothing to do.
			return err
		}

		if ll != nil {
			ll.Printf("error starting HTTP debug server, %d attempt(s) remaining: %v", attempts-(i+1), err)
		}
	}

	return errors.New("timed out starting HTTP debug server")
}

// A watcherTask is a Task which watches for link state changes.
type watcherTask struct {
	watch func(ctx context.Context) error
	ll    *log.Logger
}

// Run implements Task.
func (t *watcherTask) Run(ctx context.Context) error {
	if err := t.watch(ctx); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Watcher not available on this OS, nothing to do.
			t.ll.Printf("cannot watch for network state changes, skipping: %v", err)
			return nil
		}

		return fmt.Errorf("failed to watch for interface state changes: %v", err)
	}

	return nil
}

// Ready implements Task.
func (*watcherTask) Ready() <-chan struct{} {
	// No readiness notification, so immediately close the channel.
	c := make(chan struct{})
	close(c)
	return c
}

// String implements Task.
func (*watcherTask) String() string { return "link state watcher" }

// linkStateWatcher returns a function meant for use with errgroup.Group.Go
// which will watch for cancelation or changes on watchC.
func linkStateWatcher(ctx context.Context, watchC <-chan netstate.Change) func() error {
	return func() error {
		if watchC == nil {
			// Nothing to do.
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-watchC:
			if !ok {
				// Watcher halted or not available on this OS.
				return nil
			}

			// TODO: inspect for specific state changes.

			// Watcher indicated a state change.
			return system.ErrLinkChange
		}
	}
}

// A watcherTask is a Task which controls Server cancelation when a signal is
// received.
type signalTask struct {
	sigC   chan os.Signal
	cancel func()
	ll     *log.Logger
	n      *sdnotify.Notifier
	t      *terminator
}

// Run implements Task.
func (t *signalTask) Run(ctx context.Context) error {
	var sig os.Signal
	select {
	case <-ctx.Done():
		// Another goroutine returned an error.
		return nil
	case sig = <-t.sigC:
		// We received a shutdown signal.
	}

	t.t.set(sig)
	msg := fmt.Sprintf("received %s, shutting down", sig)
	t.ll.Print(msg)
	_ = t.n.Notify(sdnotify.Statusf(msg), sdnotify.Stopping)
	t.cancel()

	// Stop handling signals at this point to allow the user to forcefully
	// terminate the binary. It seems there is no harm in calling this function
	// with a channel that was never used with signal.Notify, so this is also
	// fine in test code.
	signal.Stop(t.sigC)
	return nil
}

// Ready implements Task.
func (*signalTask) Ready() <-chan struct{} {
	// No readiness notification, so immediately close the channel.
	c := make(chan struct{})
	close(c)
	return c
}

// String implements Task.
func (*signalTask) String() string { return "signal watcher" }

// A terminator uses a termination signal to determine whether the server is
// halting completely or is being reloaded by a process supervisor.
type terminator struct {
	mu   sync.Mutex
	term bool
}

// set uses the input signal to determine termination state.
func (t *terminator) set(s os.Signal) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.term = isTerminal(s)
}

// terminate tells server components whether or not they should completely
// terminate.
func (t *terminator) terminate() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.term
}
