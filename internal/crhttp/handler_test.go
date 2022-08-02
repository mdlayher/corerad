// Copyright 2020-2022 Matt Layher
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

package crhttp

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/ndp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestHandlerRoutes(t *testing.T) {
	tests := []struct {
		name              string
		state             system.State
		ifaces            []config.Interface
		prometheus, pprof bool
		path              string
		status            int
		check             func(t *testing.T, header http.Header, body []byte)
	}{
		{
			name:   "index",
			path:   "/",
			status: http.StatusOK,
			check: func(t *testing.T, h http.Header, body []byte) {
				if diff := cmp.Diff(contentText, h.Get("Content-Type")); diff != "" {
					t.Fatalf("unexpected Content-Type (-want +got):\n%s", diff)
				}

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
			check: func(t *testing.T, _ http.Header, body []byte) {
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
			check: func(t *testing.T, _ http.Header, body []byte) {
				if !bytes.HasPrefix(body, []byte("goroutine profile:")) {
					t.Fatal("goroutine profile was not found")
				}
			},
		},
		{
			name: "no interfaces",
			state: system.TestState{
				Forwarding: true,
			},
			path:   "/_/api/interfaces",
			status: http.StatusOK,
			check: func(t *testing.T, _ http.Header, b []byte) {
				body := parseJSONBody(b)

				if diff := cmp.Diff(0, len(body.Interfaces)); diff != "" {
					t.Fatalf("unexpected number of interfaces in HTTP body (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "interfaces",
			state: system.TestState{
				Forwarding: true,
			},
			ifaces: []config.Interface{
				// One interface in each advertising and non-advertising state.
				{
					Name:            "eth0",
					Advertise:       true,
					HopLimit:        64,
					DefaultLifetime: 30 * time.Minute,
					ReachableTime:   12345 * time.Millisecond,
					Plugins: []plugin.Plugin{
						&plugin.LLA{Addr: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad}},
						plugin.NewMTU(1500),
						&plugin.Prefix{
							Autonomous:        true,
							ValidLifetime:     10 * time.Minute,
							PreferredLifetime: 5 * time.Minute,
							Prefix:            netip.MustParsePrefix("2001:db8::/64"),
						},
						&plugin.Prefix{
							OnLink:            true,
							Autonomous:        true,
							ValidLifetime:     10 * time.Minute,
							PreferredLifetime: 5 * time.Minute,
							Prefix:            netip.MustParsePrefix("fdff:dead:beef:dead::/64"),
						},
						&plugin.DNSSL{
							Lifetime:    1 * time.Hour,
							DomainNames: []string{"lan.example.com"},
						},
						&plugin.RDNSS{
							Lifetime: 1 * time.Hour,
							Servers: []netip.Addr{
								netip.MustParseAddr("2001:db8::1"),
								netip.MustParseAddr("2001:db8::2"),
							},
						},
						&plugin.Route{
							Prefix:     netip.MustParsePrefix("2001:db8:ffff::/48"),
							Preference: ndp.High,
							Lifetime:   10 * time.Minute,
						},
						plugin.UnrestrictedPortal(),
					},
				},
				{
					Name:      "eth1",
					Advertise: false,
				},
			},
			path:   "/_/api/interfaces",
			status: http.StatusOK,
			check: func(t *testing.T, h http.Header, b []byte) {
				want := interfacesBody{
					Interfaces: []interfaceBody{
						{
							Interface:   "eth0",
							Advertising: true,
							Advertisement: &routerAdvertisement{
								CurrentHopLimit:           64,
								RouterSelectionPreference: "medium",
								RouterLifetimeSeconds:     60 * 30,
								ReachableTimeMilliseconds: 12345,
								Options: options{
									DNSSL: []dnssl{{
										LifetimeSeconds: 60 * 60,
										DomainNames:     []string{"lan.example.com"},
									}},
									MTU: 1500,
									Prefixes: []prefix{
										{
											Prefix:                             "2001:db8::/64",
											AutonomousAddressAutoconfiguration: true,
											ValidLifetimeSeconds:               60 * 10,
											PreferredLifetimeSeconds:           60 * 5,
										},
										{
											Prefix:                             "fdff:dead:beef:dead::/64",
											OnLink:                             true,
											AutonomousAddressAutoconfiguration: true,
											ValidLifetimeSeconds:               60 * 10,
											PreferredLifetimeSeconds:           60 * 5,
										},
									},
									RDNSS: []rdnss{{
										LifetimeSeconds: 60 * 60,
										Servers:         []string{"2001:db8::1", "2001:db8::2"},
									}},
									Routes: []route{{
										Prefix:               "2001:db8:ffff::/48",
										Preference:           "high",
										RouteLifetimeSeconds: 60 * 10,
									}},
									SourceLinkLayerAddress: "de:ad:be:ef:de:ad",
									CaptivePortal:          ndp.Unrestricted,
								},
							},
						},
						{
							Interface:   "eth1",
							Advertising: false,
						},
					},
				}

				if diff := cmp.Diff(contentJSON, h.Get("Content-Type")); diff != "" {
					t.Fatalf("unexpected Content-Type (-want +got):\n%s", diff)
				}

				if diff := cmp.Diff(want, parseJSONBody(b)); diff != "" {
					t.Fatalf("unexpected raBody (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "error fetching forwarding",
			state: system.TestState{
				Forwarding: true,
				Error:      os.ErrPermission,
			},
			ifaces: []config.Interface{
				{Name: "eth0", Advertise: true},
			},
			path:   "/_/api/interfaces",
			status: http.StatusInternalServerError,
			check: func(t *testing.T, _ http.Header, b []byte) {
				if !bytes.HasPrefix(b, []byte(`failed to check interface "eth0" forwarding`)) {
					t.Fatalf("unexpected body output: %s", string(b))
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
			reg.MustRegister(collectors.NewGoCollector())

			srv := httptest.NewServer(
				NewHandler(
					log.New(io.Discard, "", 0),
					tt.state,
					config.Config{
						Interfaces: tt.ifaces,
						Debug: config.Debug{
							Prometheus: tt.prometheus,
							PProf:      tt.pprof,
						},
					},
					promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
				),
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
			body, err := io.ReadAll(io.LimitReader(res.Body, 2*mb))
			if err != nil {
				t.Fatalf("failed to read HTTP body: %v", err)
			}

			tt.check(t, res.Header, body)
		})
	}
}

func parseJSONBody(b []byte) interfacesBody {
	var body interfacesBody
	if err := json.Unmarshal(b, &body); err != nil {
		panicf("failed to unmarshal JSON: %v", err)
	}

	return body
}
