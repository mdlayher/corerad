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

package corerad

import (
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/ndp"
)

func Test_verifyRAs(t *testing.T) {
	var (
		prefix = []ndp.Option{
			&ndp.PrefixInformation{
				Prefix:            mustNetIP("2001:db8::"),
				PrefixLength:      64,
				PreferredLifetime: 1 * time.Second,
				ValidLifetime:     2 * time.Second,
			},
		}

		route = []ndp.Option{
			&ndp.RouteInformation{
				Prefix:        mustNetIP("2001:db8:ffff::"),
				PrefixLength:  64,
				RouteLifetime: 1 * time.Second,
				Preference:    ndp.High,
			},
		}

		rdnss = []ndp.Option{
			&ndp.RecursiveDNSServer{
				Servers: []net.IP{
					mustNetIP("2001:db8::1"),
					mustNetIP("2001:db8::2"),
				},
			},
		}

		dnssl = []ndp.Option{
			&ndp.DNSSearchList{
				Lifetime:    10 * time.Second,
				DomainNames: []string{"foo.example.com"},
			},
		}

		full = &ndp.RouterAdvertisement{
			CurrentHopLimit:      64,
			ManagedConfiguration: true,
			OtherConfiguration:   true,
			ReachableTime:        30 * time.Minute,
			RetransmitTimer:      60 * time.Minute,
			Options: []ndp.Option{
				// Use some options from above.
				prefix[0],
				&ndp.PrefixInformation{
					PrefixLength:      32,
					OnLink:            true,
					PreferredLifetime: 10 * time.Second,
					ValidLifetime:     20 * time.Second,
					Prefix:            mustNetIP("fdff:dead:beef::"),
				},
				route[0],
				&ndp.RouteInformation{
					PrefixLength:  96,
					RouteLifetime: 10 * time.Second,
					Prefix:        mustNetIP("fdff:dead:beef::"),
				},
				rdnss[0],
				dnssl[0],
				ndp.NewMTU(1500),
			},
		}
	)

	tests := []struct {
		name     string
		a, b     *ndp.RouterAdvertisement
		problems []problem
	}{
		{
			name:     "hop limit",
			a:        &ndp.RouterAdvertisement{CurrentHopLimit: 1},
			b:        &ndp.RouterAdvertisement{CurrentHopLimit: 2},
			problems: []problem{*newProblem("hop_limit", "", 1, 2)},
		},
		{
			name:     "managed",
			a:        &ndp.RouterAdvertisement{ManagedConfiguration: true},
			b:        &ndp.RouterAdvertisement{ManagedConfiguration: false},
			problems: []problem{*newProblem("managed_configuration", "", true, false)},
		},
		{
			name:     "other",
			a:        &ndp.RouterAdvertisement{OtherConfiguration: true},
			b:        &ndp.RouterAdvertisement{OtherConfiguration: false},
			problems: []problem{*newProblem("other_configuration", "", true, false)},
		},
		{
			name:     "reachable time",
			a:        &ndp.RouterAdvertisement{ReachableTime: 1 * time.Second},
			b:        &ndp.RouterAdvertisement{ReachableTime: 2 * time.Second},
			problems: []problem{*newProblem("reachable_time", "", 1*time.Second, 2*time.Second)},
		},
		{
			name:     "retransmit timer",
			a:        &ndp.RouterAdvertisement{RetransmitTimer: 1 * time.Second},
			b:        &ndp.RouterAdvertisement{RetransmitTimer: 2 * time.Second},
			problems: []problem{*newProblem("retransmit_timer", "", 1*time.Second, 2*time.Second)},
		},
		{
			name: "MTU",
			a: &ndp.RouterAdvertisement{
				Options: []ndp.Option{ndp.NewMTU(1500)},
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{ndp.NewMTU(9000)},
			},
			problems: []problem{*newProblem("mtu", "", 1500, 9000)},
		},
		{
			name: "prefix lifetimes",
			a: &ndp.RouterAdvertisement{
				Options: prefix,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						Prefix:            mustNetIP("2001:db8::"),
						PrefixLength:      64,
						PreferredLifetime: 3 * time.Second,
						ValidLifetime:     4 * time.Second,
					},
				},
			},
			problems: []problem{
				*newProblem("prefix_information_preferred_lifetime", "2001:db8::/64", 1*time.Second, 3*time.Second),
				*newProblem("prefix_information_valid_lifetime", "2001:db8::/64", 2*time.Second, 4*time.Second),
			},
		},
		{
			name: "route, same preference different lifetime",
			a: &ndp.RouterAdvertisement{
				Options: route,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RouteInformation{
						Prefix:        mustNetIP("2001:db8:ffff::"),
						PrefixLength:  64,
						RouteLifetime: 3 * time.Second,
						Preference:    ndp.High,
					},
				},
			},
			problems: []problem{*newProblem("route_information_lifetime", "2001:db8:ffff::/64", 1*time.Second, 3*time.Second)},
		},
		{
			name: "RDNSS length",
			a: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{},
				},
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{},
					&ndp.RecursiveDNSServer{},
				},
			},
			problems: []problem{*newProblem("rdnss_count", "", 1, 2)},
		},
		{
			name: "RDNSS lifetime",
			a: &ndp.RouterAdvertisement{
				Options: rdnss,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Servers:  []net.IP{mustNetIP("2001:db8::1"), mustNetIP("2001:db8::2")},
						Lifetime: 2 * time.Second,
					},
				},
			},
			problems: []problem{*newProblem("rdnss_lifetime", "", 0*time.Second, 2*time.Second)},
		},
		{
			name: "RDNSS server length",
			a: &ndp.RouterAdvertisement{
				Options: rdnss,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{},
				},
			},
			problems: []problem{*newProblem("rdnss_servers", "", "2001:db8::1, 2001:db8::2", "")},
		},
		{
			name: "RDNSS server IPs",
			a: &ndp.RouterAdvertisement{
				Options: rdnss,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Servers: []net.IP{mustNetIP("2001:db8::2"), mustNetIP("2001:db8::3")},
					},
				},
			},
			problems: []problem{*newProblem("rdnss_servers", "", "2001:db8::1, 2001:db8::2", "2001:db8::2, 2001:db8::3")},
		},
		{
			name: "DNSSL length",
			a: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.DNSSearchList{},
				},
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.DNSSearchList{},
					&ndp.DNSSearchList{},
				},
			},
			problems: []problem{*newProblem("dnssl_count", "", 1, 2)},
		},
		{
			name: "DNSSL lifetime",
			a: &ndp.RouterAdvertisement{
				Options: dnssl,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.DNSSearchList{
						DomainNames: []string{"foo.example.com"},
						Lifetime:    2 * time.Second,
					},
				},
			},
			problems: []problem{*newProblem("dnssl_lifetime", "", 10*time.Second, 2*time.Second)},
		},
		{
			name: "DNSSL domains length",
			a: &ndp.RouterAdvertisement{
				Options: dnssl,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.DNSSearchList{
						Lifetime:    10 * time.Second,
						DomainNames: []string{"bar.example.com", "baz.example.com"},
					},
				},
			},
			problems: []problem{*newProblem("dnssl_domain_names", "", "foo.example.com", "bar.example.com, baz.example.com")},
		},
		{
			name: "DNSSL domains",
			a: &ndp.RouterAdvertisement{
				Options: dnssl,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.DNSSearchList{
						Lifetime:    10 * time.Second,
						DomainNames: []string{"bar.example.com"},
					},
				},
			},
			problems: []problem{*newProblem("dnssl_domain_names", "", "foo.example.com", "bar.example.com")},
		},
		{
			name: "OK, reachable time unspecified",
			a:    &ndp.RouterAdvertisement{},
			b:    &ndp.RouterAdvertisement{ReachableTime: 1 * time.Second},
		},
		{
			name: "OK, retransmit timer unspecified",
			a:    &ndp.RouterAdvertisement{},
			b:    &ndp.RouterAdvertisement{RetransmitTimer: 1 * time.Second},
		},
		{
			name: "OK, MTU unspecified",
			a: &ndp.RouterAdvertisement{
				Options: []ndp.Option{ndp.NewMTU(1500)},
			},
			b: &ndp.RouterAdvertisement{},
		},
		{
			name: "OK, prefix unspecified",
			a: &ndp.RouterAdvertisement{
				Options: prefix,
			},
			b: &ndp.RouterAdvertisement{},
		},
		{
			name: "OK, prefix different",
			a: &ndp.RouterAdvertisement{
				Options: prefix,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						Prefix:            mustNetIP("fdff:dead:beef::"),
						PrefixLength:      64,
						PreferredLifetime: 3 * time.Second,
						ValidLifetime:     4 * time.Second,
					},
				},
			},
		},
		{
			name: "OK, route unspecified",
			a: &ndp.RouterAdvertisement{
				Options: route,
			},
			b: &ndp.RouterAdvertisement{},
		},
		{
			name: "OK, route different",
			a: &ndp.RouterAdvertisement{
				Options: route,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RouteInformation{
						Prefix:        mustNetIP("fdff:dead:beef::"),
						PrefixLength:  64,
						RouteLifetime: 3 * time.Second,
					},
				},
			},
		},
		{
			name: "OK, route preference different",
			a: &ndp.RouterAdvertisement{
				Options: route,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RouteInformation{
						Prefix:        mustNetIP("2001:db8:ffff::"),
						PrefixLength:  64,
						RouteLifetime: 1 * time.Second,
						Preference:    ndp.Low,
					},
				},
			},
		},
		{
			name: "OK, RDNSS unspecified",
			a: &ndp.RouterAdvertisement{
				Options: rdnss,
			},
			b: &ndp.RouterAdvertisement{},
		},
		{
			name: "OK, DNSSL unspecified",
			a: &ndp.RouterAdvertisement{
				Options: dnssl,
			},
			b: &ndp.RouterAdvertisement{},
		},
		{
			name: "OK, all",
			a:    full,
			b:    full,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.problems, verifyRAs(tt.a, tt.b)); diff != "" {
				t.Fatalf("unexpected router advertisement problems (-want +got):\n%s", diff)
			}
		})
	}
}
