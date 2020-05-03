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
	}{
		{
			name:   "index",
			path:   "/",
			status: http.StatusOK,
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
		},
		{
			name:   "pprof disabled",
			path:   "/debug/pprof/",
			status: http.StatusNotFound,
		},
		{
			name:   "pprof enabled",
			pprof:  true,
			path:   "/debug/pprof/",
			status: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(
				crhttp.NewHandler(tt.prometheus, tt.pprof, &prometheus.Registry{}),
			)
			defer srv.Close()

			u, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatalf("failed to parse URL: %v", err)
			}
			u.Path = tt.path

			c := &http.Client{Timeout: 2 * time.Second}
			res, err := c.Get(u.String())
			if err != nil {
				t.Fatalf("failed to HTTP GET: %v", err)
			}

			if diff := cmp.Diff(tt.status, res.StatusCode); diff != "" {
				t.Fatalf("unexpected HTTP status code (-want +got):\n%s", diff)
			}
		})
	}
}
