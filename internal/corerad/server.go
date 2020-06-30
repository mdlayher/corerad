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
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/crhttp"
	"github.com/mdlayher/corerad/internal/netstate"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/metricslite"
	"github.com/mdlayher/sdnotify"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
)

// A Server coordinates the goroutines that handle various pieces of the
// CoreRAD server.
type Server struct {
	state system.State
	w     *netstate.Watcher

	ll  *log.Logger
	reg *prometheus.Registry
}

// NewServer creates a Server with the input configuration and logger. If ll
// is nil, logs are discarded.
func NewServer(ll *log.Logger) *Server {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	// TODO: parameterize if needed.
	state := system.NewState()

	// Set up Prometheus instrumentation using the typical Go collectors.
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(
		prometheus.NewBuildInfoCollector(),
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	return &Server{
		state: state,
		w:     netstate.NewWatcher(),

		ll:  ll,
		reg: reg,
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

// BuildTasks produces Tasks for the Server to run from the input configuration.
func (s *Server) BuildTasks(cfg config.Config) []Task {
	mm := NewMetrics(metricslite.NewPrometheus(s.reg), s.state, cfg.Interfaces)

	// Serve on each specified interface.
	var tasks []Task
	for _, ifi := range cfg.Interfaces {
		if !ifi.Advertise && !ifi.Monitor {
			s.ll.Printf("%s: interface is not advertising or monitoring, skipping initialization", ifi.Name)
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
			dialer := system.NewDialer(ifi.Name, s.state, system.Advertise, s.ll)

			tasks = append(
				tasks,
				NewAdvertiser(ifi, dialer, s.state, watchC, s.ll, mm),
			)
		case ifi.Monitor:
			dialer := system.NewDialer(ifi.Name, s.state, system.Monitor, s.ll)

			tasks = append(
				tasks,
				NewMonitor(ifi.Name, dialer, watchC, ifi.Verbose, s.ll, mm),
			)
		default:
			panicf("corerad: Server interface %q is not advertising or monitoring", ifi.Name)
		}
	}

	// Optionally configure the debug HTTP server task.
	if d := cfg.Debug; d.Address != "" {
		s.ll.Printf("starting HTTP debug listener on %q: prometheus: %t, pprof: %t",
			d.Address, d.Prometheus, d.PProf)

		tasks = append(tasks, &httpTask{
			addr: d.Address,
			h: crhttp.NewHandler(
				s.ll,
				s.state,
				cfg.Interfaces,
				d.Prometheus,
				d.PProf,
				s.reg,
			),
			ll:     s.ll,
			readyC: make(chan struct{}),
		})
	}

	// Optionally configure the link state watcher task.
	if s.w != nil {
		tasks = append(tasks, &watcherTask{
			watch: s.w.Watch,
			ll:    s.ll,
		})
	}

	return tasks
}

// Serve starts the CoreRAD server and runs Tasks until the context is canceled.
func (s *Server) Serve(ctx context.Context, n *sdnotify.Notifier, tasks []Task) error {
	// Attach the context to the errgroup so that goroutines are canceled when
	// one of them returns an error.
	eg, ctx := errgroup.WithContext(ctx)

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
