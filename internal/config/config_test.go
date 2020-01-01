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
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
)

func TestParse(t *testing.T) {
	t.Parallel()

	defaultPrefix := config.NewPrefix()

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
			name: "bad plugin empty name",
			s: `
			[[interfaces]]
			name = "eth0"

			  [[interfaces.plugins]]
			`,
		},
		{
			name: "bad plugin name",
			s: `
			[[interfaces]]
			name = "eth0"

			  [[interfaces.plugins]]
			  name = "bad"
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
			name: "OK no plugins",
			s: `
			[[interfaces]]
			name = "eth0"
			send_advertisements = true
			default_lifetime = ""
			`,
			c: &config.Config{
				Interfaces: []config.Interface{{
					Name:               "eth0",
					SendAdvertisements: true,
					MinInterval:        3*time.Minute + 18*time.Second,
					MaxInterval:        10 * time.Minute,
					Plugins:            []config.Plugin{},
				}},
			},
			ok: true,
		},
		{
			name: "OK all",
			s: `
			[[interfaces]]
			name = "eth0"
			send_advertisements = true
			max_interval = "10m"
			min_interval = "6m"
			hop_limit = 64
			default_lifetime = "auto"

			  [[interfaces.plugins]]
			  name = "prefix"
			  prefix = "::/64"

			  [[interfaces.plugins]]
			  name = "prefix"
			  prefix = "2001:db8::/64"
			  autonomous = false

			  [[interfaces.plugins]]
			  name = "rdnss"
			  lifetime = "auto"
			  servers = ["2001:db8::1"]

			  [[interfaces.plugins]]
			  name = "dnssl"
			  lifetime = "auto"
			  domain_names = ["lan.example.com"]

			[[interfaces]]
			name = "eth1"
			min_interval = "auto"
			max_interval = "4s"
			default_lifetime = "8s"
			managed = true
			other_config = true
			reachable_time = "30s"
			retransmit_timer = "5s"

			[debug]
			address = "localhost:9430"
			prometheus = true
			pprof = true
			`,
			c: &config.Config{
				Interfaces: []config.Interface{
					{
						Name:               "eth0",
						SendAdvertisements: true,
						MinInterval:        6 * time.Minute,
						MaxInterval:        10 * time.Minute,
						HopLimit:           64,
						DefaultLifetime:    30 * time.Minute,
						Plugins: []config.Plugin{
							&config.Prefix{
								Prefix:            mustCIDR("::/64"),
								OnLink:            defaultPrefix.OnLink,
								Autonomous:        defaultPrefix.Autonomous,
								ValidLifetime:     defaultPrefix.ValidLifetime,
								PreferredLifetime: defaultPrefix.PreferredLifetime,
							},
							&config.Prefix{
								Prefix:            mustCIDR("2001:db8::/64"),
								OnLink:            defaultPrefix.OnLink,
								Autonomous:        false,
								ValidLifetime:     defaultPrefix.ValidLifetime,
								PreferredLifetime: defaultPrefix.PreferredLifetime,
							},
							&config.RDNSS{
								Lifetime: config.DurationAuto,
								Servers:  []net.IP{mustIP("2001:db8::1")},
							},
							&config.DNSSL{
								Lifetime:    config.DurationAuto,
								DomainNames: []string{"lan.example.com"},
							},
						},
					},
					{
						Name:               "eth1",
						SendAdvertisements: false,
						MinInterval:        4 * time.Second,
						MaxInterval:        4 * time.Second,
						Managed:            true,
						OtherConfig:        true,
						ReachableTime:      30 * time.Second,
						RetransmitTimer:    5 * time.Second,
						DefaultLifetime:    8 * time.Second,
						Plugins:            []config.Plugin{},
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
			c, err := config.Parse(strings.NewReader(tt.s))
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

			if diff := cmp.Diff(tt.c, c); diff != "" {
				t.Fatalf("unexpected Config (-want +got):\n%s", diff)
			}
		})
	}
}

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panicf("failed to parse %q as IP address", s)
	}

	return ip
}

func mustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
