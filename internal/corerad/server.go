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
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/pprof"

	"github.com/mdlayher/corerad/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

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

	// Serve on each specified interface.
	for _, ifi := range s.cfg.Interfaces {
		// Prepend the interface name to all logs for this server.
		logf := func(format string, v ...interface{}) {
			s.ll.Println(ifi.Name + ": " + fmt.Sprintf(format, v...))
		}

		if !ifi.SendAdvertisements {
			logf("send advertisements is false, skipping initialization")
			continue
		}

		logf("initializing with %d plugins", len(ifi.Plugins))

		for i, p := range ifi.Plugins {
			logf("plugin %02d: %q: %s", i, p.Name(), p)
		}

		// TODO: find a way to reasonably test this.

		// Begin advertising on this interface until the context is canceled.
		ad, err := NewAdvertiser(ifi, s.ll)
		if err != nil {
			return fmt.Errorf("failed to create NDP advertiser: %v", err)
		}

		s.eg.Go(func() error {
			if err := ad.Advertise(ctx); err != nil {
				return fmt.Errorf("failed to advertise NDP: %v", err)
			}

			return nil
		})
	}

	// Configure the HTTP debug server, if applicable.
	if err := s.runDebug(ctx); err != nil {
		return fmt.Errorf("failed to start debug HTTP server: %v", err)
	}

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

	// Configure the HTTP debug server.
	l, err := net.Listen("tcp", d.Address)
	if err != nil {
		return fmt.Errorf("failed to start debug listener: %v", err)
	}

	s.ll.Printf("starting HTTP debug listener on %q: prometheus: %v, pprof: %v",
		d.Address, d.Prometheus, d.PProf)

	// Serve requests until the context is canceled.
	s.eg.Go(func() error {
		<-ctx.Done()
		return l.Close()
	})

	s.eg.Go(func() error {
		return serve(http.Serve(
			l, newHTTPHandler(d.Prometheus, d.PProf, s.reg),
		))
	})

	return nil
}

// serve unpacks and handles certain network listener errors as appropriate.
func serve(err error) error {
	if err == nil {
		return nil
	}

	nerr, ok := err.(*net.OpError)
	if !ok {
		return err
	}

	// Unfortunately there isn't an easier way to check for this, but
	// we want to ignore errors related to the connection closing, since
	// s.Close is triggered on signal.
	if nerr.Err.Error() != "use of closed network connection" {
		return err
	}

	return nil
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
