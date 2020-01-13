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

package corerad_test

import (
	"bytes"
	"context"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/corerad"
	"github.com/mdlayher/promtest"
	"golang.org/x/sync/errgroup"
)

func TestServerRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.Config

		// fn is a test case. cancel must be invoked by fn. debug specifies the
		// the address string for the HTTP debug server.
		fn func(t *testing.T, cancel func(), debug string)
	}{
		{
			name: "no configuration",
		},
		{
			name: "debug no prometheus or pprof",
			cfg: config.Config{
				Debug: config.Debug{
					Address: randAddr(t),
				},
			},
			fn: func(t *testing.T, cancel func(), debug string) {
				defer cancel()

				// Debug listener should start, but Prometheus and pprof
				// endpoints should not.
				if !probeTCP(t, debug) {
					t.Fatal("debug listener did not start")
				}

				prom := httpGet(t, debug+"/metrics")
				if diff := cmp.Diff(http.StatusNotFound, prom.StatusCode); diff != "" {
					t.Fatalf("unexpected Prometheus HTTP status (-want +got):\n%s", diff)
				}

				pprof := httpGet(t, debug+"/debug/pprof/")
				if diff := cmp.Diff(http.StatusNotFound, pprof.StatusCode); diff != "" {
					t.Fatalf("unexpected pprof HTTP status (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "debug prometheus and pprof",
			cfg: config.Config{
				Debug: config.Debug{
					Address:    randAddr(t),
					Prometheus: true,
					PProf:      true,
				},
			},
			fn: func(t *testing.T, cancel func(), debug string) {
				defer cancel()

				// Debug listener should start with both configured endpoints
				// available.
				if !probeTCP(t, debug) {
					t.Fatal("debug listener did not start")
				}

				pprof := httpGet(t, debug+"/debug/pprof/")
				if diff := cmp.Diff(http.StatusOK, pprof.StatusCode); diff != "" {
					t.Fatalf("unexpected pprof HTTP status (-want +got):\n%s", diff)
				}

				prom := httpGet(t, debug+"/metrics")
				defer prom.Body.Close()

				if diff := cmp.Diff(http.StatusOK, prom.StatusCode); diff != "" {
					t.Fatalf("unexpected Prometheus HTTP status (-want +got):\n%s", diff)
				}

				b, err := ioutil.ReadAll(prom.Body)
				if err != nil {
					t.Fatalf("failed to read Prometheus metrics: %v", err)
				}

				// Validate the necessary metrics.
				if !promtest.Lint(t, b) {
					t.Fatal("Prometheus metrics are not lint-clean")
				}

				// Check for specific metrics.
				want := []string{
					// TODO.
				}

				for _, w := range want {
					if !bytes.Contains(b, []byte(w)) {
						t.Errorf("prometheus metrics do not contain %q", w)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			s := corerad.NewServer(tt.cfg, nil)

			debug := tt.cfg.Debug.Address

			var eg errgroup.Group
			eg.Go(func() error {
				return s.Run(ctx)
			})

			// Ensure the server has time to fully set up before we run tests.
			<-s.Ready()

			if tt.fn == nil {
				// If no function specified, just cancel the server immediately.
				cancel()
			} else {
				tt.fn(t, cancel, debug)
			}

			if err := eg.Wait(); err != nil {
				t.Fatalf("failed to run server: %v", err)
			}

			// All services should be stopped.
			if probeTCP(t, debug) {
				t.Fatal("debug server still running")
			}
		})
	}
}

func probeTCP(t *testing.T, addr string) bool {
	t.Helper()

	// As a convenience, if the address is empty, we know that the service
	// cannot be probed.
	if addr == "" {
		return false
	}

	const (
		attempts = 4
		delay    = 250 * time.Millisecond
	)

	for i := 0; i < attempts; i++ {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close()
			return true
		}

		// String comparison isn't great but using build tags for syscall errno
		// seems like overkill.
		nerr, ok := err.(*net.OpError)
		if !ok || !strings.Contains(nerr.Err.Error(), "connection refused") {
			t.Fatalf("failed to dial TCP: %v", err)
		}

		time.Sleep(delay)
	}

	return false
}

func httpGet(t *testing.T, addr string) *http.Response {
	t.Helper()

	addr = "http://" + addr
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatalf("failed to parse URL: %v", err)
	}

	c := &http.Client{Timeout: 1 * time.Second}
	res, err := c.Get(u.String())
	if err != nil {
		t.Fatalf("failed to HTTP GET: %v", err)
	}

	return res
}

func randAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	_ = l.Close()

	return l.Addr().String()
}
