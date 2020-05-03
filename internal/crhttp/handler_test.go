// Copyright 2020 Matt Layher
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

package crhttp_test

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/crhttp"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHandlerRoutes(t *testing.T) {
	tests := []struct {
		name              string
		prometheus, pprof bool
		path              string
		status            int
		check             func(t *testing.T, body []byte)
	}{
		{
			name:   "index",
			path:   "/",
			status: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				if !bytes.HasPrefix(body, []byte("CoreRAD")) {
					t.Fatal("CoreRAD banner was not found")
				}
			},
		},
		{
			name:   "not found",
			path:   "/foo",
			status: http.StatusNotFound,
		},
		{
			name:   "prometheus disabled",
			path:   "/metrics",
			status: http.StatusNotFound,
		},
		{
			name:       "prometheus enabled",
			prometheus: true,
			path:       "/metrics",
			status:     http.StatusOK,
			check: func(t *testing.T, body []byte) {
				if !bytes.HasPrefix(body, []byte("# HELP go_")) {
					t.Fatal("Prometheus Go collector metric was not found")
				}
			},
		},
		{
			name:   "pprof disabled",
			path:   "/debug/pprof/",
			status: http.StatusNotFound,
		},
		{
			name:   "pprof enabled",
			pprof:  true,
			path:   "/debug/pprof/goroutine?debug=1",
			status: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				if !bytes.HasPrefix(body, []byte("goroutine profile:")) {
					t.Fatal("goroutine profile was not found")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a Prometheus registry with a known-good set of metrics
			// that we can check for when the Prometheus functionality is
			// enabled.
			reg := prometheus.NewPedanticRegistry()
			reg.MustRegister(prometheus.NewGoCollector())

			srv := httptest.NewServer(
				crhttp.NewHandler(tt.prometheus, tt.pprof, reg),
			)
			defer srv.Close()

			// Use string contenation rather than path.Join because we want to
			// not escape any raw query parameters provided as part of the path.
			u, err := url.Parse(srv.URL + tt.path)
			if err != nil {
				t.Fatalf("failed to parse URL: %v", err)
			}

			c := &http.Client{Timeout: 2 * time.Second}
			res, err := c.Get(u.String())
			if err != nil {
				t.Fatalf("failed to HTTP GET: %v", err)
			}
			defer res.Body.Close()

			if diff := cmp.Diff(tt.status, res.StatusCode); diff != "" {
				t.Fatalf("unexpected HTTP status code (-want +got):\n%s", diff)
			}

			// If set, apply an additional sanity check on the response body.
			if tt.check == nil {
				return
			}

			// Don't consume a stream larger than a sane upper bound.
			const mb = 1 << 20
			body, err := ioutil.ReadAll(io.LimitReader(res.Body, 2*mb))
			if err != nil {
				t.Fatalf("failed to read HTTP body: %v", err)
			}

			tt.check(t, body)
		})
	}
}
