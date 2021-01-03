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

package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/crtest"
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
			preference = "low"

			[[interfaces]]
			name = "eth2"
			verbose = true
			hop_limit = 0
			unicast_only = true
			source_lla = false
			preference = "high"

			[[interfaces]]
			name = "eth3"
			monitor = true
			verbose = true

			[[interfaces]]
			name = "eth4"
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
								Prefix:            crtest.MustIPPrefix("::/64"),
								OnLink:            true,
								Autonomous:        true,
								ValidLifetime:     24 * time.Hour,
								PreferredLifetime: 4 * time.Hour,
							},
							&plugin.Prefix{
								Prefix:            crtest.MustIPPrefix("2001:db8::/64"),
								OnLink:            true,
								Autonomous:        false,
								ValidLifetime:     24 * time.Hour,
								PreferredLifetime: 4 * time.Hour,
								Deprecated:        true,
							},
							&plugin.Route{
								Prefix:     crtest.MustIPPrefix("2001:db8:ffff::/64"),
								Preference: ndp.Medium,
								Lifetime:   24 * time.Hour,
							},
							&plugin.RDNSS{
								Lifetime: 20 * time.Minute,
								Servers:  []netaddr.IP{crtest.MustIP("2001:db8::1")},
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
						Plugins:         []plugin.Plugin{&plugin.LLA{}},
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
						Plugins:         []plugin.Plugin{},
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

			if diff := cmp.Diff(tt.c, c, cmp.Comparer(compareNetaddrIP)); diff != "" {
				t.Fatalf("unexpected Config (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseDefaults(t *testing.T) {
	t.Parallel()

	const minimal = `
		[[interfaces]]
		name = "eth0"

		  [[interfaces.prefix]]
		  prefix = "::/64"

		  [[interfaces.prefix]]
		  prefix = "2001:db8::/64"

		  [[interfaces.route]]
		  prefix = "2001:db8:ffff::/64"

		  [[interfaces.rdnss]]
		  servers = ["2001:db8::1", "2001:db8::2"]

		  [[interfaces.dnssl]]
		  domain_names = ["foo.example.com"]

		[debug]
		address = "localhost:9430"
	`

	min, err := config.Parse(strings.NewReader(minimal), time.Time{})
	if err != nil {
		t.Fatalf("failed to parse minimal config: %v", err)
	}
	defaults, err := config.Parse(strings.NewReader(config.Default), time.Time{})
	if err != nil {
		t.Fatalf("failed to parse default config: %v", err)
	}

	if diff := cmp.Diff(defaults, min, cmp.Comparer(compareNetaddrIP)); diff != "" {
		t.Fatalf("unexpected default Config (-want +got):\n%s", diff)
	}
}

func TestInterfaceRouterAdvertisement(t *testing.T) {
	// More comprehensive tests exist in internal/corerad; just check the
	// basics.

	tests := []struct {
		name       string
		ifi        config.Interface
		forwarding bool
		ra         *ndp.RouterAdvertisement
	}{
		{
			name: "no forwarding",
			ifi: config.Interface{
				HopLimit:        64,
				Preference:      ndp.High,
				DefaultLifetime: 30 * time.Minute,
				Plugins:         []plugin.Plugin{plugin.NewMTU(1500)},
			},
			ra: &ndp.RouterAdvertisement{
				CurrentHopLimit:           64,
				RouterSelectionPreference: ndp.High,
				RouterLifetime:            0,
				Options:                   []ndp.Option{ndp.NewMTU(1500)},
			},
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
			ra, err := tt.ifi.RouterAdvertisement(tt.forwarding)
			if err != nil {
				t.Fatalf("failed to generate router advertisement: %v", err)
			}

			if diff := cmp.Diff(tt.ra, ra); diff != "" {
				t.Fatalf("unexpected router advertisement (-want +got):\n%s", diff)
			}
		})
	}
}

func compareNetaddrIP(x, y netaddr.IP) bool { return x == y }
