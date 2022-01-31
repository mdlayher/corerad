// Copyright 2019-2022 Matt Layher
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

package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/ndp"
	"inet.af/netaddr"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		c    *config.Config
		ok   bool
	}{
		{
			name: "bad TOML",
			s:    "xxx",
		},
		{
			name: "bad keys",
			s: `
			[bad]
			[[bad.bad]]
			`,
		},
		{
			name: "bad no interfaces",
			s:    ``,
		},
		{
			name: "bad empty interface",
			s: `
			[[interfaces]]
			name = ""
			`,
		},
		{
			name: "bad interface name and names",
			s: `
			[[interfaces]]
			name = "foo"
			names = ["foo"]
			`,
		},
		{
			name: "bad interface names repeated",
			s: `
			[[interfaces]]
			names = ["foo", "foo"]
			`,
		},
		{
			name: "bad interface name repeated",
			s: `
			[[interfaces]]
			name = "foo"
			[[interfaces]]
			name = "foo"
			`,
		},
		{
			name: "bad interface",
			s: `
			[[interfaces]]
			name = "eth0"
			default_lifetime = "yes"
			`,
		},
		{
			name: "bad debug address",
			s: `
			[[interfaces]]
			name = "eth0"
			[debug]
			address = "xxx"
			`,
		},
		{
			name: "OK minimal defaults",
			s: `
			[[interfaces]]
			name = "eth0"
			`,
			c: &config.Config{
				Interfaces: []config.Interface{{
					Name:            "eth0",
					Monitor:         false,
					Advertise:       false,
					MinInterval:     3*time.Minute + 18*time.Second,
					MaxInterval:     10 * time.Minute,
					HopLimit:        64,
					DefaultLifetime: 30 * time.Minute,
					UnicastOnly:     false,
					Preference:      ndp.Medium,
					Plugins:         []plugin.Plugin{&plugin.LLA{}},
				}},
			},
			ok: true,
		},
		{
			name: "OK all",
			s: `
			[[interfaces]]
			name = "eth0"
			advertise = true
			max_interval = "10m"
			min_interval = "6m"
			hop_limit = 64
			default_lifetime = "auto"
			mtu = 1500
			preference = "medium"

			  [[interfaces.prefix]]
			  prefix = "::/64"

			  [[interfaces.prefix]]
			  prefix = "2001:db8::/64"
			  autonomous = false
			  deprecated = true

			  [[interfaces.route]]
			  prefix = "2001:db8:ffff::/64"

			  [[interfaces.rdnss]]
			  lifetime = "auto"
			  servers = ["2001:db8::1"]

			  [[interfaces.dnssl]]
			  lifetime = "auto"
			  domain_names = ["lan.example.com"]

			[[interfaces]]
			name = "eth1"
			min_interval = "auto"
			max_interval = "4s"
			# default hop_limit.
			default_lifetime = "8s"
			managed = true
			other_config = true
			reachable_time = "30s"
			retransmit_timer = "5s"
			source_lla = true
			captive_portal = ""
			preference = "low"

			  [[interfaces.rdnss]]
			  servers = ["::"]

			[[interfaces]]
			name = "eth2"
			verbose = true
			hop_limit = 0
			unicast_only = true
			source_lla = false
			captive_portal = "http://router/portal"
			preference = "high"

			[[interfaces]]
			name = "eth3"
			monitor = true
			verbose = true

			[[interfaces]]
			names = ["eth4", "eth5"]
			advertise = true
			default_lifetime = ""

			[debug]
			address = "localhost:9430"
			prometheus = true
			pprof = true
			`,
			c: &config.Config{
				Interfaces: []config.Interface{
					{
						Name:            "eth0",
						Advertise:       true,
						MinInterval:     6 * time.Minute,
						MaxInterval:     10 * time.Minute,
						HopLimit:        64,
						DefaultLifetime: 30 * time.Minute,
						Preference:      ndp.Medium,
						UnicastOnly:     false,
						Plugins: []plugin.Plugin{
							&plugin.Prefix{
								Auto:              true,
								Prefix:            netaddr.MustParseIPPrefix("::/64"),
								OnLink:            true,
								Autonomous:        true,
								ValidLifetime:     24 * time.Hour,
								PreferredLifetime: 4 * time.Hour,
							},
							&plugin.Prefix{
								Prefix:            netaddr.MustParseIPPrefix("2001:db8::/64"),
								OnLink:            true,
								Autonomous:        false,
								ValidLifetime:     24 * time.Hour,
								PreferredLifetime: 4 * time.Hour,
								Deprecated:        true,
							},
							&plugin.Route{
								Prefix:     netaddr.MustParseIPPrefix("2001:db8:ffff::/64"),
								Preference: ndp.Medium,
								Lifetime:   24 * time.Hour,
							},
							&plugin.RDNSS{
								Lifetime: 20 * time.Minute,
								Servers:  []netaddr.IP{netaddr.MustParseIP("2001:db8::1")},
							},
							&plugin.DNSSL{
								Lifetime:    20 * time.Minute,
								DomainNames: []string{"lan.example.com"},
							},
							plugin.NewMTU(1500),
							&plugin.LLA{},
						},
					},
					{
						Name:            "eth1",
						Advertise:       false,
						MinInterval:     4 * time.Second,
						MaxInterval:     4 * time.Second,
						HopLimit:        64,
						Managed:         true,
						OtherConfig:     true,
						ReachableTime:   30 * time.Second,
						RetransmitTimer: 5 * time.Second,
						DefaultLifetime: 8 * time.Second,
						Preference:      ndp.Low,
						Plugins: []plugin.Plugin{
							&plugin.RDNSS{
								Auto:     true,
								Lifetime: 8 * time.Second,
							},
							&plugin.LLA{},
						},
					},
					{
						Name:            "eth2",
						Advertise:       false,
						Verbose:         true,
						MinInterval:     3*time.Minute + 18*time.Second,
						MaxInterval:     10 * time.Minute,
						HopLimit:        0,
						DefaultLifetime: 30 * time.Minute,
						UnicastOnly:     true,
						Preference:      ndp.High,
						Plugins:         []plugin.Plugin{plugin.NewCaptivePortal("http://router/portal")},
					},
					{
						Name:    "eth3",
						Monitor: true,
						Verbose: true,
					},
					{
						Name:        "eth4",
						Advertise:   true,
						MinInterval: 3*time.Minute + 18*time.Second,
						MaxInterval: 10 * time.Minute,
						HopLimit:    64,
						Plugins:     []plugin.Plugin{&plugin.LLA{}},
					},
					{
						Name:        "eth5",
						Advertise:   true,
						MinInterval: 3*time.Minute + 18*time.Second,
						MaxInterval: 10 * time.Minute,
						HopLimit:    64,
						Plugins:     []plugin.Plugin{&plugin.LLA{}},
					},
				},
				Debug: config.Debug{
					Address:    "localhost:9430",
					Prometheus: true,
					PProf:      true,
				},
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.Parse(strings.NewReader(tt.s), time.Time{})
			if tt.ok && err != nil {
				t.Fatalf("failed to parse config: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				t.Logf("err: %v", err)
				return
			}

			opts := []cmp.Option{
				cmp.Comparer(ipEqual), cmp.Comparer(ipPrefixEqual),
			}

			if diff := cmp.Diff(tt.c, c, opts...); diff != "" {
				t.Fatalf("unexpected Config (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseMinimal(t *testing.T) {
	t.Parallel()

	if _, err := config.Parse(strings.NewReader(config.Minimal), time.Time{}); err != nil {
		t.Fatalf("failed to parse minimal config: %v", err)
	}
}

func TestParseReference(t *testing.T) {
	t.Parallel()

	f, err := os.Open("reference.toml")
	if err != nil {
		t.Fatalf("failed to open reference config: %v", err)
	}
	defer f.Close()

	if _, err := config.Parse(f, time.Time{}); err != nil {
		t.Fatalf("failed to parse reference config: %v", err)
	}
}

func TestInterfaceRouterAdvertisement(t *testing.T) {
	t.Parallel()

	// More comprehensive tests exist in internal/corerad; just check the
	// basics.

	tests := []struct {
		name       string
		ifi        config.Interface
		forwarding bool
		ra         *ndp.RouterAdvertisement
		ms         []config.Misconfiguration
	}{
		{
			name: "no forwarding",
			ifi: config.Interface{
				HopLimit:   64,
				Preference: ndp.High,
				Plugins:    []plugin.Plugin{plugin.NewMTU(1500)},
			},
			ra: &ndp.RouterAdvertisement{
				CurrentHopLimit:           64,
				RouterSelectionPreference: ndp.High,
				Options:                   []ndp.Option{ndp.NewMTU(1500)},
			},
		},
		{
			name: "no forwarding, misconfigured",
			ifi: config.Interface{
				DefaultLifetime: 30 * time.Minute,
			},
			ra: &ndp.RouterAdvertisement{
				RouterLifetime: 0,
			},
			ms: []config.Misconfiguration{config.InterfaceNotForwarding},
		},
		{
			name: "forwarding",
			ifi: config.Interface{
				HopLimit:        64,
				DefaultLifetime: 30 * time.Minute,
			},
			forwarding: true,
			ra: &ndp.RouterAdvertisement{
				CurrentHopLimit: 64,
				RouterLifetime:  30 * time.Minute,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ra, ms, err := tt.ifi.RouterAdvertisement(tt.forwarding)
			if err != nil {
				t.Fatalf("failed to generate router advertisement: %v", err)
			}

			if diff := cmp.Diff(tt.ra, ra); diff != "" {
				t.Errorf("unexpected router advertisement (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.ms, ms); diff != "" {
				t.Fatalf("unexpected misconfigurations (-want +got):\n%s", diff)
			}
		})
	}
}

func ipEqual(x, y netaddr.IP) bool             { return x == y }
func ipPrefixEqual(x, y netaddr.IPPrefix) bool { return x == y }
