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

package plugin

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/ndp"
)

func TestPluginString(t *testing.T) {
	// A function which returns synthesized interface addresses. Address
	// prefixes are duplicated and of different address types to exercise
	// different test cases for wildcard options.
	addrs := func() ([]system.IP, error) {
		return []system.IP{
			{Address: netip.MustParsePrefix("2001:db8::/64")},
			{Address: netip.MustParsePrefix("2001:db8::1/64")},
			{Address: netip.MustParsePrefix("fdff::/64")},
			{Address: netip.MustParsePrefix("fdff::1/64")},
			{Address: netip.MustParsePrefix("fe80::/64")},
			{Address: netip.MustParsePrefix("fe80::1/64")},
		}, nil
	}

	tests := []struct {
		name string
		p    Plugin
		s    string
	}{
		{
			name: "Captive Portal",
			p:    NewCaptivePortal("http://router/portal"),
			s:    `URI: "http://router/portal"`,
		},
		{
			name: "DNSSL",
			p: &DNSSL{
				Lifetime:    30 * time.Second,
				DomainNames: []string{"foo.example.com", "bar.example.com"},
			},
			s: "domain names: [foo.example.com, bar.example.com], lifetime: 30s",
		},
		{
			name: "LLA nil",
			p:    &LLA{},
			s:    "source link-layer address: n/a",
		},
		{
			name: "LLA",
			p:    &LLA{Addr: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad}},
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
				Prefix:            netip.MustParsePrefix("2001:db8::/64"),
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
				Auto:              true,
				Prefix:            netip.MustParsePrefix("::/64"),
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
				Prefix:     netip.MustParsePrefix("2001:db8::/64"),
				Preference: ndp.High,
				Lifetime:   15 * time.Minute,
				Deprecated: true,
			},
			s: "2001:db8::/64 [DEPRECATED], preference: High, lifetime: 15m0s",
		},
		{
			name: "Route wildcard",
			p: &Route{
				Auto:     true,
				Prefix:   netip.MustParsePrefix("::/0"),
				Lifetime: 10 * time.Minute,
				Routes: func() ([]system.Route, error) {
					return []system.Route{
						{Prefix: netip.MustParsePrefix("fd00::/48")},
						{Prefix: netip.MustParsePrefix("2001:db8::/32")},
					}, nil
				},
			},
			s: "::/0 [2001:db8::/32, fd00::/48], preference: Medium, lifetime: 10m0s",
		},
		{
			name: "RDNSS",
			p: &RDNSS{
				Lifetime: 30 * time.Second,
				Servers: []netip.Addr{
					netip.MustParseAddr("2001:db8::1"),
					netip.MustParseAddr("2001:db8::2"),
				},
			},
			s: "servers: [2001:db8::1, 2001:db8::2], lifetime: 30s",
		},
		{
			name: "RDNSS wildcard",
			p: &RDNSS{
				Auto:     true,
				Lifetime: 30 * time.Second,
				Servers:  []netip.Addr{netip.IPv6Unspecified()},
				Addrs:    addrs,
			},
			s: "servers: [:: [fdff::]], lifetime: 30s",
		},
		{
			name: "RDNSS wildcard and static",
			p: &RDNSS{
				Auto:     true,
				Lifetime: 30 * time.Second,
				Servers: []netip.Addr{
					netip.IPv6Unspecified(),
					netip.MustParseAddr("2001:db8::1"),
				},
				Addrs: addrs,
			},
			s: "servers: [:: [fdff::], 2001:db8::1], lifetime: 30s",
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
			name:   "CaptivePortal",
			plugin: NewCaptivePortal("http://router/portal"),
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{ndp.NewCaptivePortal("http://router/portal")},
			},
			ok: true,
		},
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
				Prefix:            netip.MustParsePrefix("2001:db8::/32"),
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
						Prefix:            netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic prefixes /64",
			plugin: &Prefix{
				Auto:              true,
				Prefix:            netip.MustParsePrefix("::/64"),
				OnLink:            true,
				Autonomous:        true,
				PreferredLifetime: 10 * time.Second,
				ValidLifetime:     20 * time.Second,

				Addrs: func() ([]system.IP, error) {
					return []system.IP{
						// Populate some addresses that should be ignored.
						{Address: netip.MustParsePrefix("192.0.2.1/24")},
						{Address: netip.MustParsePrefix("fe80::1/64")},
						{Address: netip.MustParsePrefix("fdff::1/32")},
						{
							Address:   netip.MustParsePrefix("2001:db8::fff0/64"),
							Temporary: true,
						},
						{
							Address:   netip.MustParsePrefix("2001:db8::fff1/64"),
							Tentative: true,
						},
						// Addresses which are not ignored.
						{Address: netip.MustParsePrefix("2001:db8::1/64")},
						{Address: netip.MustParsePrefix("2001:db8::2/64")},
						{Address: netip.MustParsePrefix("fd00::1/64")},
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
						Prefix:                         netip.MustParseAddr("2001:db8::"),
					},
					&ndp.PrefixInformation{
						PrefixLength:                   64,
						OnLink:                         true,
						AutonomousAddressConfiguration: true,
						PreferredLifetime:              10 * time.Second,
						ValidLifetime:                  20 * time.Second,
						Prefix:                         netip.MustParseAddr("fd00::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic prefixes /32",
			plugin: &Prefix{
				Auto:   true,
				Prefix: netip.MustParsePrefix("::/32"),
				Addrs: func() ([]system.IP, error) {
					return []system.IP{
						// Specify an IPv4 address that could feasibly be
						// matched, but must be skipped due to incorrect address
						// family.
						{Address: netip.MustParsePrefix("192.0.2.1/32")},
						{Address: netip.MustParsePrefix("2001:db8::1/32")},
					}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						PrefixLength: 32,
						Prefix:       netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "prefix deprecated preferred and valid",
			plugin: &Prefix{
				Prefix:            netip.MustParsePrefix("2001:db8::/64"),
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
						Prefix:                         netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "prefix deprecated valid",
			plugin: &Prefix{
				Prefix:            netip.MustParsePrefix("2001:db8::/64"),
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
						Prefix:                         netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "prefix deprecated invalid",
			plugin: &Prefix{
				Prefix:            netip.MustParsePrefix("2001:db8::/64"),
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
						Prefix:                         netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "route",
			plugin: &Route{
				Prefix:     netip.MustParsePrefix("2001:db8::/32"),
				Preference: ndp.High,
				Lifetime:   10 * time.Second,
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RouteInformation{
						PrefixLength:  32,
						Preference:    ndp.High,
						RouteLifetime: 10 * time.Second,
						Prefix:        netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic routes",
			plugin: &Route{
				Auto:     true,
				Lifetime: 10 * time.Second,
				Routes: func() ([]system.Route, error) {
					return []system.Route{
						// IPv4 or /128s should be skipped.
						{Prefix: netip.MustParsePrefix("192.0.2.1/32")},
						{Prefix: netip.MustParsePrefix("::1/128")},
						// The /48 is contained within the /32 and should be
						// skipped.
						{Prefix: netip.MustParsePrefix("2001:db8:ffff::/48")},
						{Prefix: netip.MustParsePrefix("2001:db8::/32")},
					}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RouteInformation{
						PrefixLength:  32,
						Preference:    ndp.Medium,
						RouteLifetime: 10 * time.Second,
						Prefix:        netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "route deprecated valid",
			plugin: &Route{
				Prefix:   netip.MustParsePrefix("2001:db8::/32"),
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
						Prefix:        netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "route deprecated invalid",
			plugin: &Route{
				Prefix:   netip.MustParsePrefix("2001:db8::/32"),
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
						Prefix:        netip.MustParseAddr("2001:db8::"),
					},
				},
			},
			ok: true,
		},
		{
			name: "static RDNSS",
			plugin: &RDNSS{
				Lifetime: 10 * time.Second,
				Servers: []netip.Addr{
					netip.MustParseAddr("2001:db8::1"),
					netip.MustParseAddr("2001:db8::2"),
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers: []netip.Addr{
							netip.MustParseAddr("2001:db8::1"),
							netip.MustParseAddr("2001:db8::2"),
						},
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic RDNSS no addresses",
			plugin: &RDNSS{
				Auto:     true,
				Lifetime: 10 * time.Second,
				Addrs:    func() ([]system.IP, error) { return nil, nil },
			},
		},
		{
			name: "automatic RDNSS one address",
			plugin: &RDNSS{
				Auto:     true,
				Lifetime: 10 * time.Second,
				Addrs: func() ([]system.IP, error) {
					return []system.IP{{
						Address: netip.MustParsePrefix("2001:db8::1/64"),
					}}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers:  []netip.Addr{netip.MustParseAddr("2001:db8::1")},
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic RDNSS many addresses",
			plugin: &RDNSS{
				Auto:     true,
				Lifetime: 10 * time.Second,
				Addrs: func() ([]system.IP, error) {
					return []system.IP{
						// Populate some addresses which should be ignored.
						// Normally ULAs win the tie-breaker so use those
						// explicitly for bad IPv6 addresses.
						{Address: netip.MustParsePrefix("192.0.2.1/32")},
						{
							Address:    netip.MustParsePrefix("fd00::fff0/64"),
							Deprecated: true,
						},
						{
							Address:   netip.MustParsePrefix("fd00::fff1/64"),
							Temporary: true,
						},
						{
							Address:   netip.MustParsePrefix("fd00::fff2/64"),
							Tentative: true,
						},
						// Addresses which are not ignored.
						{Address: netip.MustParsePrefix("fdff::1/64")},
						{Address: netip.MustParsePrefix("2001:db8::1/64")},
						// Winner.
						{
							Address:                  netip.MustParsePrefix("fdff::10/64"),
							ManageTemporaryAddresses: true,
						},
					}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers:  []netip.Addr{netip.MustParseAddr("fdff::10")},
					},
				},
			},
			ok: true,
		},
		{
			name: "automatic RDNSS with static address",
			plugin: &RDNSS{
				Auto:     true,
				Lifetime: 10 * time.Second,
				Servers:  []netip.Addr{netip.MustParseAddr("2001:db8::2")},
				Addrs: func() ([]system.IP, error) {
					return []system.IP{{
						Address: netip.MustParsePrefix("2001:db8::1/64"),
					}}, nil
				},
			},
			ra: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers:  []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::2")},
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

			if diff := cmp.Diff(tt.ra, ra, cmp.Comparer(addrEqual)); diff != "" {
				t.Fatalf("unexpected RA (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_betterRDNSS(t *testing.T) {
	var (
		lo = system.IP{Address: netip.MustParsePrefix("::1/128")}

		ula1   = system.IP{Address: netip.MustParsePrefix("fdff::1/64")}
		ula2   = system.IP{Address: netip.MustParsePrefix("fdff::2/64")}
		ulaMTA = system.IP{
			Address:                  netip.MustParsePrefix("fdff::3/64"),
			ManageTemporaryAddresses: true,
		}
		ulaForever = system.IP{
			Address:      netip.MustParsePrefix("fdff::4/64"),
			ValidForever: true,
		}

		gua1      = system.IP{Address: netip.MustParsePrefix("2001:db8::1/64")}
		gua2      = system.IP{Address: netip.MustParsePrefix("2001:db8::2/64")}
		guaStable = system.IP{
			Address:       netip.MustParsePrefix("2001:db8::3/64"),
			StablePrivacy: true,
		}

		lla1     = system.IP{Address: netip.MustParsePrefix("fe80::1/64")}
		lla2     = system.IP{Address: netip.MustParsePrefix("fe80::2/64")}
		llaEUI64 = system.IP{
			Address: netip.MustParsePrefix("fe80::f:ff:fe00:ffff/64"),
		}
	)

	tests := []struct {
		name                  string
		best, current, better system.IP
	}{
		{
			name:    "zero best",
			current: ula1,
			best:    system.IP{},
			better:  ula1,
		},
		{
			name:    "self best",
			current: ula1,
			best:    ula1,
			better:  ula1,
		},
		{
			name:    "zero vs zero",
			current: system.IP{},
			best:    system.IP{},
			better:  system.IP{},
		},
		{
			name:    "lo vs lo",
			current: lo,
			best:    lo,
			better:  lo,
		},
		{
			name:    "ULA vs ULA",
			current: ula1,
			best:    ula2,
			better:  ula1,
		},
		{
			name:    "ULA vs ULA MTA",
			current: ula1,
			best:    ulaMTA,
			better:  ulaMTA,
		},
		{
			name:    "ULA vs GUA",
			current: ula1,
			best:    gua1,
			better:  ula1,
		},
		{
			name:    "ULA vs LLA",
			current: ula1,
			best:    lla1,
			better:  ula1,
		},
		{
			name:    "ULA MTA vs ULA MTA",
			current: ulaMTA,
			best:    ulaMTA,
			better:  ulaMTA,
		},
		{
			name:    "ULA MTA vs GUA stable",
			current: ulaMTA,
			best:    guaStable,
			better:  ulaMTA,
		},
		{
			name:    "GUA vs ULA",
			current: gua1,
			best:    ula1,
			better:  ula1,
		},
		{
			name:    "GUA vs GUA",
			current: gua1,
			best:    gua2,
			better:  gua1,
		},
		{
			name:    "GUA stable vs GUA",
			current: guaStable,
			best:    gua1,
			better:  guaStable,
		},
		{
			name:    "GUA vs LLA",
			current: gua1,
			best:    lla1,
			better:  gua1,
		},
		{
			name:    "LLA vs ULA",
			current: lla1,
			best:    ula1,
			better:  ula1,
		},
		{
			name:    "LLA vs GUA",
			current: lla1,
			best:    gua1,
			better:  gua1,
		},
		{
			name:    "LLA vs LLA",
			current: lla1,
			best:    lla2,
			better:  lla1,
		},
		{
			name:    "LLA vs LLA EUI-64",
			current: lla1,
			best:    llaEUI64,
			better:  llaEUI64,
		},
		{
			name:    "ULA vs LLA EUI-64",
			current: ulaForever,
			best:    llaEUI64,
			better:  ulaForever,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.better, betterRDNSS(tt.best, tt.current), cmp.Comparer(ipEqual)); diff != "" {
				t.Fatalf("unexpected better result (-want +got):\n%s", diff)
			}
		})
	}
}

func addrEqual(x, y netip.Addr) bool { return x == y }
func ipEqual(x, y system.IP) bool    { return x == y }
