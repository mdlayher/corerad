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

package plugin

import (
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/ndp"
	"inet.af/netaddr"
)

func TestPluginString(t *testing.T) {
	// A function which returns synthesized interface addresses. Address
	// prefixes are duplicated and of different address types to exercise
	// different test cases for wildcard options.
	addrs := func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{
				IP:   net.ParseIP("2001:db8::"),
				Mask: net.CIDRMask(64, 128),
			},
			&net.IPNet{
				IP:   net.ParseIP("2001:db8::1"),
				Mask: net.CIDRMask(64, 128),
			},
			&net.IPNet{
				IP:   net.ParseIP("fdff::"),
				Mask: net.CIDRMask(64, 128),
			},
			&net.IPNet{
				IP:   net.ParseIP("fdff::1"),
				Mask: net.CIDRMask(64, 128),
			},
			&net.IPNet{
				IP:   net.ParseIP("fe80::"),
				Mask: net.CIDRMask(64, 128),
			},
			&net.IPNet{
				IP:   net.ParseIP("fe80::1"),
				Mask: net.CIDRMask(64, 128),
			},
		}, nil
	}

	tests := []struct {
		name string
		p    Plugin
		s    string
	}{
		{
			name: "DNSSL",
			p: &DNSSL{
				Lifetime:    30 * time.Second,
				DomainNames: []string{"foo.example.com", "bar.example.com"},
			},
			s: "domain names: [foo.example.com, bar.example.com], lifetime: 30s",
		},
		{
			name: "LLA",
			p:    &LLA{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad},
			s:    "source link-layer address: de:ad:be:ef:de:ad",
		},
		{
			name: "MTU",
			p:    NewMTU(1500),
			s:    "MTU: 1500",
		},
		{
			name: "Prefix",
			p: &Prefix{
				Prefix:            netaddr.MustParseIPPrefix("2001:db8::/64"),
				OnLink:            true,
				Autonomous:        true,
				PreferredLifetime: 15 * time.Minute,
				ValidLifetime:     ndp.Infinity,
			},
			s: "2001:db8::/64 [on-link, autonomous], preferred: 15m0s, valid: infinite",
		},
		{
			name: "Prefix wildcard",
			p: &Prefix{
				Prefix:            netaddr.MustParseIPPrefix("::/64"),
				OnLink:            true,
				Autonomous:        true,
				PreferredLifetime: 15 * time.Minute,
				ValidLifetime:     ndp.Infinity,
				Deprecated:        true,
				Addrs:             addrs,
			},
			s: "::/64 [2001:db8::/64, fdff::/64] [DEPRECATED, on-link, autonomous], preferred: 15m0s, valid: infinite",
		},
		{
			name: "Route",
			p: &Route{
				Prefix:     netaddr.MustParseIPPrefix("2001:db8::/64"),
				Preference: ndp.High,
				Lifetime:   15 * time.Minute,
				Deprecated: true,
			},
			s: "2001:db8::/64 [DEPRECATED], preference: High, lifetime: 15m0s",
		},
		{
			name: "RDNSS",
			p: &RDNSS{
				Lifetime: 30 * time.Second,
				Servers: []netaddr.IP{
					netaddr.MustParseIP("2001:db8::1"),
					netaddr.MustParseIP("2001:db8::2"),
				},
			},
			s: "servers: [2001:db8::1, 2001:db8::2], lifetime: 30s",
		},
		{
			name: "RDNSS wildcard",
			p: &RDNSS{
				Lifetime: 30 * time.Second,
				Servers:  []netaddr.IP{netaddr.IPv6Unspecified()},
				Addrs:    addrs,
			},
			s: "servers: :: [fdff::], lifetime: 30s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.s, tt.p.String()); diff != "" {
				t.Fatalf("unexpected string (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name   string
		plugin Plugin
		ifi    *net.Interface
		ra     *ndp.RouterAdvertisement
		ok     bool
	}{
		{
			name: "DNSSL",
			plugin: &DNSSL{
				Lifetime: 10 * time.Second,
				DomainNames: []string{
					"foo.example.com",
					"bar.example.com",
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.DNSSearchList{
						Lifetime: 10 * time.Second,
						DomainNames: []string{
							"foo.example.com",
							"bar.example.com",
						},
					},
				},
			},
			ok: true,
		},
		{
			name:   "LLA",
			plugin: &LLA{},
			ifi: &net.Interface{
				HardwareAddr: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.LinkLayerAddress{
						Direction: ndp.Source,
						Addr:      net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad},
					},
				},
			},
			ok: true,
		},
		{
			name:   "MTU",
			plugin: NewMTU(1500),
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{ndp.NewMTU(1500)},
			},
			ok: true,
		},
		{
			name: "static prefix",
			plugin: &Prefix{
				Prefix:            netaddr.MustParseIPPrefix("2001:db8::/32"),
				OnLink:            true,
				PreferredLifetime: 10 * time.Second,
				ValidLifetime:     20 * time.Second,
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength:      32,
						OnLink:            true,
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
						Prefix:            mustIP("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic prefixes /64",
			plugin: &Prefix{
				Prefix:            netaddr.MustParseIPPrefix("::/64"),
				OnLink:            true,
				Autonomous:        true,
				PreferredLifetime: 10 * time.Second,
				ValidLifetime:     20 * time.Second,

				Addrs: func() ([]net.Addr, error) {
					return []net.Addr{
						// Populate some addresses that should be ignored.
						mustCIDR("192.0.2.1/24"),
						&net.TCPAddr{},
						mustCIDR("fe80::1/64"),
						mustCIDR("fdff::1/32"),
						mustCIDR("2001:db8::1/64"),
						mustCIDR("2001:db8::2/64"),
						mustCIDR("fd00::1/64"),
					}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength:                   64,
						OnLink:                         true,
						AutonomousAddressConfiguration: true,
						PreferredLifetime:              10 * time.Second,
						ValidLifetime:                  20 * time.Second,
						Prefix:                         mustIP("2001:db8::"),
					},
					&ndp.PrefixInformation{
						PrefixLength:                   64,
						OnLink:                         true,
						AutonomousAddressConfiguration: true,
						PreferredLifetime:              10 * time.Second,
						ValidLifetime:                  20 * time.Second,
						Prefix:                         mustIP("fd00::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic prefixes /32",
			plugin: &Prefix{
				Prefix: netaddr.MustParseIPPrefix("::/32"),
				Addrs: func() ([]net.Addr, error) {
					return []net.Addr{
						// Specify an IPv4 address that could feasibly be
						// matched, but must be skipped due to incorrect address
						// family.
						mustCIDR("192.0.2.1/32"),
						mustCIDR("2001:db8::1/32"),
					}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength: 32,
						Prefix:       mustIP("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "prefix deprecated preferred and valid",
			plugin: &Prefix{
				Prefix:            netaddr.MustParseIPPrefix("2001:db8::/64"),
				Autonomous:        true,
				OnLink:            true,
				PreferredLifetime: 10 * time.Second,
				ValidLifetime:     20 * time.Second,

				Deprecated: true,
				Epoch:      time.Unix(1, 0),
				TimeNow:    func() time.Time { return time.Unix(5, 0) },
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength:                   64,
						AutonomousAddressConfiguration: true,
						OnLink:                         true,
						PreferredLifetime:              6 * time.Second,
						ValidLifetime:                  16 * time.Second,
						Prefix:                         mustIP("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "prefix deprecated valid",
			plugin: &Prefix{
				Prefix:            netaddr.MustParseIPPrefix("2001:db8::/64"),
				Autonomous:        true,
				OnLink:            true,
				PreferredLifetime: 10 * time.Second,
				ValidLifetime:     20 * time.Second,

				Deprecated: true,
				Epoch:      time.Unix(1, 0),
				TimeNow:    func() time.Time { return time.Unix(11, 0) },
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength:                   64,
						AutonomousAddressConfiguration: true,
						OnLink:                         true,
						PreferredLifetime:              0 * time.Second,
						ValidLifetime:                  10 * time.Second,
						Prefix:                         mustIP("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "prefix deprecated invalid",
			plugin: &Prefix{
				Prefix:            netaddr.MustParseIPPrefix("2001:db8::/64"),
				Autonomous:        true,
				OnLink:            true,
				PreferredLifetime: 10 * time.Second,
				ValidLifetime:     20 * time.Second,

				Deprecated: true,
				Epoch:      time.Unix(1, 0),
				TimeNow:    func() time.Time { return time.Unix(21, 0) },
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength:                   64,
						AutonomousAddressConfiguration: true,
						OnLink:                         true,
						PreferredLifetime:              0 * time.Second,
						ValidLifetime:                  0 * time.Second,
						Prefix:                         mustIP("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "route",
			plugin: &Route{
				Prefix:     netaddr.MustParseIPPrefix("2001:db8::/32"),
				Preference: ndp.High,
				Lifetime:   10 * time.Second,
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RouteInformation{
						PrefixLength:  32,
						Preference:    ndp.High,
						RouteLifetime: 10 * time.Second,
						Prefix:        mustIP("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "route deprecated valid",
			plugin: &Route{
				Prefix:   netaddr.MustParseIPPrefix("2001:db8::/32"),
				Lifetime: 10 * time.Second,

				Deprecated: true,
				Epoch:      time.Unix(1, 0),
				TimeNow:    func() time.Time { return time.Unix(5, 0) },
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RouteInformation{
						PrefixLength:  32,
						RouteLifetime: 6 * time.Second,
						Prefix:        mustIP("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "route deprecated invalid",
			plugin: &Route{
				Prefix:   netaddr.MustParseIPPrefix("2001:db8::/32"),
				Lifetime: 10 * time.Second,

				Deprecated: true,
				Epoch:      time.Unix(1, 0),
				TimeNow:    func() time.Time { return time.Unix(11, 0) },
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RouteInformation{
						PrefixLength:  32,
						RouteLifetime: 0 * time.Second,
						Prefix:        mustIP("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "static RDNSS",
			plugin: &RDNSS{
				Lifetime: 10 * time.Second,
				Servers: []netaddr.IP{
					netaddr.MustParseIP("2001:db8::1"),
					netaddr.MustParseIP("2001:db8::2"),
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers: []net.IP{
							mustIP("2001:db8::1"),
							mustIP("2001:db8::2"),
						},
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic RDNSS no addresses",
			plugin: &RDNSS{
				Lifetime: 10 * time.Second,
				Servers:  []netaddr.IP{netaddr.IPv6Unspecified()},
				Addrs:    func() ([]net.Addr, error) { return nil, nil },
			},
		},
		{
			name: "automatic RDNSS one address",
			plugin: &RDNSS{
				Lifetime: 10 * time.Second,
				Servers:  []netaddr.IP{netaddr.IPv6Unspecified()},
				Addrs: func() ([]net.Addr, error) {
					return []net.Addr{mustCIDR("2001:db8::1/64")}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers:  []net.IP{mustIP("2001:db8::1")},
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic RDNSS many addresses",
			plugin: &RDNSS{
				Lifetime: 10 * time.Second,
				Servers:  []netaddr.IP{netaddr.IPv6Unspecified()},
				Addrs: func() ([]net.Addr, error) {
					return []net.Addr{
						// Populate some addresses which should be ignored.
						&net.TCPAddr{},
						mustCIDR("192.0.2.1/32"),
						mustCIDR("fdff::1/64"),
						mustCIDR("2001:db8::1/64"),
					}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers:  []net.IP{mustIP("fdff::1")},
					},
				},
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ra := new(ndp.RouterAdvertisement)

			if tt.ifi != nil {
				if err := tt.plugin.Prepare(tt.ifi); err != nil {
					t.Fatalf("failed to prepare: %v", err)
				}
			}

			err := tt.plugin.Apply(ra)
			if tt.ok && err != nil {
				t.Fatalf("failed to apply: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				t.Logf("err: %v", err)
				return
			}

			if diff := cmp.Diff(tt.ra, ra); diff != "" {
				t.Fatalf("unexpected RA (-want +got):\n%s", diff)
			}

		})
	}
}

func Test_betterRDNSS(t *testing.T) {
	var (
		lo = netaddr.MustParseIP("::1")

		ula1 = netaddr.MustParseIP("fdff::1")
		ula2 = netaddr.MustParseIP("fdff::2")

		gua1 = netaddr.MustParseIP("2001:db8::1")
		gua2 = netaddr.MustParseIP("2001:db8::2")

		lla1 = netaddr.MustParseIP("fe80::1")
		lla2 = netaddr.MustParseIP("fe80::2")
	)

	tests := []struct {
		name          string
		best, current netaddr.IP
		ok            bool
	}{
		{
			name:    "zero best",
			current: ula1,
			best:    netaddr.IP{},
			ok:      true,
		},
		{
			name:    "self best",
			current: ula1,
			best:    ula1,
			ok:      false,
		},
		{
			name:    "zero vs zero",
			current: netaddr.IP{},
			best:    netaddr.IP{},
			ok:      true,
		},
		{
			name:    "lo vs lo",
			current: lo,
			best:    lo,
			ok:      false,
		},
		{
			name:    "ULA vs ULA",
			current: ula1,
			best:    ula2,
			ok:      true,
		},
		{
			name:    "ULA vs GUA",
			current: ula1,
			best:    gua1,
			ok:      true,
		},
		{
			name:    "ULA vs LLA",
			current: ula1,
			best:    lla1,
			ok:      true,
		},
		{
			name:    "GUA vs ULA",
			current: gua1,
			best:    ula1,
			ok:      false,
		},
		{
			name:    "GUA vs GUA",
			current: gua1,
			best:    gua2,
			ok:      true,
		},
		{
			name:    "GUA vs LLA",
			current: gua1,
			best:    lla1,
			ok:      true,
		},
		{
			name:    "LLA vs ULA",
			current: lla1,
			best:    ula1,
			ok:      false,
		},
		{
			name:    "LLA vs GUA",
			current: lla1,
			best:    gua1,
			ok:      false,
		},
		{
			name:    "LLA vs LLA",
			current: lla1,
			best:    lla2,
			ok:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.ok, betterRDNSS(tt.best, tt.current)); diff != "" {
				t.Fatalf("unexpected better result for %s vs %s (-want +got):\n%s", tt.best, tt.current, diff)
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
	ip, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	// Remove masking to simulate 2001:db8::1/64 and etc. properly.
	ipn.IP = ip
	return ipn
}
