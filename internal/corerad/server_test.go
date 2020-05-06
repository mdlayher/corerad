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
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/corerad"
	"golang.org/x/sync/errgroup"
)

func TestServerRun(t *testing.T) {
	t.Parallel()

	// This test covers basic server setup behaviors, but should not cover
	// in-depth test cases for the Advertiser or HTTP server.

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
			name: "debug HTTP",
			cfg: config.Config{
				Debug: config.Debug{
					Address: randAddr(t),
				},
			},
			fn: func(t *testing.T, cancel func(), debug string) {
				defer cancel()

				res := httpGet(t, debug)
				if diff := cmp.Diff(http.StatusOK, res.StatusCode); diff != "" {
					t.Fatalf("unexpected debug HTTP status (-want +got):\n%s", diff)
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
		})
	}
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
