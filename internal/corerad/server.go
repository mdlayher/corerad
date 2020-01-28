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
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"sync"
	"time"

	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/netstate"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

const namespace = "corerad"

// A Server coordinates the goroutines that handle various pieces of the
// CoreRAD server.
type Server struct {
	cfg config.Config

	ll  *log.Logger
	reg *prometheus.Registry

	eg    *errgroup.Group
	ready chan struct{}
}

// NewServer creates a Server with the input configuration and logger. If ll
// is nil, logs are discarded.
func NewServer(cfg config.Config, ll *log.Logger) *Server {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	// Set up Prometheus instrumentation using the typical Go collectors.
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
		newInterfaceCollector(cfg.Interfaces),
	)

	return &Server{
		cfg: cfg,

		ll:  ll,
		reg: reg,

		ready: make(chan struct{}),
	}
}

// Ready indicates that the server is ready to begin serving requests.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Run runs the corerad server until the context is canceled.
func (s *Server) Run(ctx context.Context) error {
	// Attach the context to the errgroup so that goroutines are canceled when
	// one of them returns an error.
	eg, ctx := errgroup.WithContext(ctx)
	s.eg = eg
	defer close(s.ready)

	mm := NewAdvertiserMetrics(s.reg)

	// Watch for interface state changes. May or may not be supported depending
	// on the OS, but functionality should gracefully degrade.
	w := netstate.NewWatcher()

	// Serve on each specified interface.
	for _, iface := range s.cfg.Interfaces {
		// Capture range variable for goroutines.
		iface := iface

		// Prepend the interface name to all logs for this server.
		logf := func(format string, v ...interface{}) {
			s.ll.Println(iface.Name + ": " + fmt.Sprintf(format, v...))
		}

		if !iface.SendAdvertisements {
			logf("send advertisements is false, skipping initialization")
			continue
		}

		// TODO: find a way to reasonably test this.

		// Register interest for state changes on this interface to this channel
		// so this interface's Advertiser can react accordingly.
		watchC := w.Subscribe(iface.Name, netstate.LinkAny)

		// Begin advertising on this interface until the context is canceled.
		s.eg.Go(func() error {
			ad := NewAdvertiser(iface.Name, iface, s.ll, mm)
			if err := ad.Advertise(ctx, watchC); err != nil {
				return fmt.Errorf("failed to advertise NDP: %v", err)
			}

			return nil
		})
	}

	// Configure the HTTP debug server, if applicable.
	if err := s.runDebug(ctx); err != nil {
		return fmt.Errorf("failed to start debug HTTP server: %v", err)
	}

	s.eg.Go(func() error {
		if err := w.Watch(ctx); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Watcher not available on this OS, nothing to do.
				s.ll.Printf("cannot watch for network state changes, skipping: %v", err)
				return nil
			}

			return fmt.Errorf("failed to watch for interface state changes: %v", err)
		}

		return nil
	})

	// Indicate readiness to any waiting callers, and then wait for all
	// goroutines to be canceled and stopped successfully.
	s.ready <- struct{}{}
	if err := s.eg.Wait(); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}

	return nil
}

// runDebug runs a debug HTTP server using goroutines, until ctx is canceled.
func (s *Server) runDebug(ctx context.Context) error {
	d := s.cfg.Debug
	if d.Address == "" {
		// Nothing to do, don't start the server.
		return nil
	}

	s.ll.Printf("starting HTTP debug listener on %q: prometheus: %v, pprof: %v",
		d.Address, d.Prometheus, d.PProf)

	s.eg.Go(func() error {
		// Serve the debug server with retries in the event that the configured
		// interface is not available on startup.
		return s.serve(ctx, func() error {
			l, err := net.Listen("tcp", d.Address)
			if err != nil {
				return err
			}

			// Listener ready, wait for cancelation via context and serve
			// the HTTP server.
			var wg sync.WaitGroup
			wg.Add(1)
			defer wg.Wait()

			go func() {
				defer wg.Done()
				<-ctx.Done()
				_ = l.Close()
			}()

			return http.Serve(l, newHTTPHandler(d.Prometheus, d.PProf, s.reg))
		})
	})

	return nil
}

// serve invokes fn with retries until a listener is started, handling certain
// network listener errors as appropriate.
func (s *Server) serve(ctx context.Context, fn func() error) error {
	const (
		attempts = 40
		delay    = 3 * time.Second
	)

	var nerr *net.OpError
	for i := 0; i < attempts; i++ {
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
		case errors.As(err, &nerr):
			// Handle outside switch.
		case err == nil:
			panic("corerad: serve function should never return nil")
		default:
			// Nothing to do.
			return err
		}

		// Unfortunately there isn't an easier way to check for this, but
		// we want to ignore errors related to the connection closing, since
		// s.Close is triggered on signal.
		if nerr.Err.Error() == "use of closed network connection" {
			return nil
		}

		s.ll.Printf("error starting HTTP debug server, %d attempt(s) remaining: %v", attempts-(i+1), err)
	}

	return errors.New("timed out starting HTTP debug server")
}

// A httpHandler provides the HTTP debug API handler for CoreRAD.
type httpHandler struct {
	h http.Handler
}

// NewHTTPHandler creates a httpHandler with the specified configuration.
func newHTTPHandler(
	usePrometheus, usePProf bool,
	reg *prometheus.Registry,
) *httpHandler {
	mux := http.NewServeMux()

	h := &httpHandler{
		h: mux,
	}

	// Optionally enable Prometheus and pprof support.
	if usePrometheus {
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	}

	if usePProf {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	return h
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Matching on "/" would produce an overly broad rule, so check manually
	// here and indicate that this is the CoreRAD service.
	if r.URL.Path == "/" {
		_, _ = io.WriteString(w, "CoreRAD\n")
		return
	}

	h.h.ServeHTTP(w, r)
}
