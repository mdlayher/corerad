// Copyright 2021-2022 Matt Layher
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

//go:build linux
// +build linux

package system

import (
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jsimonetti/rtnetlink"
	"github.com/mdlayher/ndp"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
	"inet.af/netaddr"
)

func TestLinux_addresserAddressesByIndex(t *testing.T) {
	// Use a fixed interface index for correct messages.
	const index = 1

	tests := []struct {
		name     string
		msgs     []rtnetlink.Message
		panicked bool
		ips      []IP
	}{
		{
			name:     "bad message type",
			msgs:     []rtnetlink.Message{&rtnetlink.LinkMessage{}},
			panicked: true,
		},
		{
			name: "missing attributes",
			msgs: []rtnetlink.Message{&rtnetlink.AddressMessage{
				Family:     unix.AF_INET6,
				Index:      index,
				Attributes: nil,
			}},
			panicked: true,
		},
		{
			name: "invalid IP",
			msgs: []rtnetlink.Message{&rtnetlink.AddressMessage{
				Family: unix.AF_INET6,
				Index:  index,
				Attributes: &rtnetlink.AddressAttributes{
					Address: nil,
				},
			}},
			panicked: true,
		},
		{
			name: "invalid IPv4",
			msgs: []rtnetlink.Message{&rtnetlink.AddressMessage{
				Family: unix.AF_INET6,
				Index:  index,
				Attributes: &rtnetlink.AddressAttributes{
					Address: net.IPv4(192, 0, 2, 1),
				},
			}},
			panicked: true,
		},
		{
			name: "empty response",
		},
		{
			name: "ok",
			msgs: []rtnetlink.Message{
				&rtnetlink.AddressMessage{
					Family:       unix.AF_INET6,
					PrefixLength: 64,
					Index:        index,
					Attributes: &rtnetlink.AddressAttributes{
						Address: net.ParseIP("2001:db8::1"),
					},
				},
				&rtnetlink.AddressMessage{
					Family:       unix.AF_INET6,
					PrefixLength: 128,
					Index:        index,
					Attributes: &rtnetlink.AddressAttributes{
						Address: net.ParseIP("fe80::1"),
						// This flag combination is nonsense but we can use it
						// to test for each bit we check.
						Flags: unix.IFA_F_DEPRECATED |
							unix.IFA_F_MANAGETEMPADDR |
							unix.IFA_F_STABLE_PRIVACY |
							unix.IFA_F_TEMPORARY |
							unix.IFA_F_TENTATIVE,
					},
				},
			},
			ips: []IP{
				{Address: netaddr.MustParseIPPrefix("2001:db8::1/64")},
				{
					Address:                  netaddr.MustParseIPPrefix("fe80::1/128"),
					Deprecated:               true,
					ManageTemporaryAddresses: true,
					StablePrivacy:            true,
					Temporary:                true,
					Tentative:                true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &addresser{
				execute: func(m rtnetlink.Message, family uint16, flags netlink.HeaderFlags) ([]rtnetlink.Message, error) {
					wantMessage := &rtnetlink.AddressMessage{
						Family: unix.AF_INET6,
						Index:  index,
					}

					if diff := cmp.Diff(wantMessage, m); diff != "" {
						t.Fatalf("unexpected request message (-want +got):\n%s", diff)
					}

					if diff := cmp.Diff(unix.RTM_GETADDR, int(family)); diff != "" {
						t.Fatalf("unexpected netlink header family (-want +got):\n%s", diff)
					}

					if diff := cmp.Diff(netlink.Request|netlink.Dump, flags); diff != "" {
						t.Fatalf("unexpected netlink header flags (-want +got):\n%s", diff)
					}

					return tt.msgs, nil
				},
			}

			var ips []IP
			panicked := panics(t, func() {
				out, err := a.AddressesByIndex(index)
				if err != nil {
					t.Fatalf("failed to get addresses: %v", err)
				}

				ips = out
			})
			if diff := cmp.Diff(tt.panicked, panicked); diff != "" {
				t.Fatalf("unexpected function panic (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.ips, ips, cmp.Comparer(ipPrefixEqual), cmp.Comparer(ipEqual)); diff != "" {
				t.Fatalf("unexpected IPs (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLinux_addresserLoopbackRoutes(t *testing.T) {
	// Use a fixed interface index for correct messages.
	const index = 1
	pref := uint8(ndp.High)

	tests := []struct {
		name     string
		msgs     []rtnetlink.Message
		panicked bool
		routes   []Route
	}{
		{
			name:     "bad message type",
			msgs:     []rtnetlink.Message{&rtnetlink.LinkMessage{}},
			panicked: true,
		},
		{
			name: "bad family",
			msgs: []rtnetlink.Message{&rtnetlink.RouteMessage{
				Family: unix.AF_INET,
			}},
			panicked: true,
		},
		{
			name: "invalid IP",
			msgs: []rtnetlink.Message{&rtnetlink.RouteMessage{
				Family:     unix.AF_INET6,
				Attributes: rtnetlink.RouteAttributes{Dst: nil},
			}},
			panicked: true,
		},
		{
			name: "invalid IPv4",
			msgs: []rtnetlink.Message{&rtnetlink.RouteMessage{
				Family: unix.AF_INET6,
				Attributes: rtnetlink.RouteAttributes{
					Dst: net.IPv4(192, 0, 2, 1),
				},
			}},
			panicked: true,
		},
		{
			name: "empty response",
		},
		{
			name: "ok",
			msgs: []rtnetlink.Message{
				&rtnetlink.RouteMessage{
					Family:    unix.AF_INET6,
					DstLength: 32,
					Attributes: rtnetlink.RouteAttributes{
						Dst:      net.ParseIP("2001:db8::"),
						OutIface: index,
					},
				},
				// Filtered later by internal/plugin package.
				&rtnetlink.RouteMessage{
					Family:    unix.AF_INET6,
					DstLength: 128,
					Attributes: rtnetlink.RouteAttributes{
						Dst:      net.IPv6loopback,
						OutIface: index,
					},
				},
				// A hypothetical second loopback interface route. This is a bit
				// strange because it's technically part of the first
				// interface's route dump but the effect is the same.
				&rtnetlink.RouteMessage{
					Family:    unix.AF_INET6,
					DstLength: 48,
					Attributes: rtnetlink.RouteAttributes{
						Dst:      net.ParseIP("fd00::"),
						OutIface: index + 1,
						Pref:     &pref,
					},
				},
			},
			routes: []Route{
				{
					Prefix: netaddr.MustParseIPPrefix("2001:db8::/32"),
					Index:  index,
				},
				{
					Prefix: netaddr.MustParseIPPrefix("::1/128"),
					Index:  index,
				},
				{
					Prefix:     netaddr.MustParseIPPrefix("fd00::/48"),
					Index:      index + 1,
					Preference: ndp.High,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &addresser{
				execute: func(m rtnetlink.Message, family uint16, flags netlink.HeaderFlags) ([]rtnetlink.Message, error) {
					wantMessage := &rtnetlink.RouteMessage{
						Family: unix.AF_INET6,
						Attributes: rtnetlink.RouteAttributes{
							OutIface: index,
							Table:    unix.RT_TABLE_MAIN,
						},
					}

					if diff := cmp.Diff(wantMessage, m); diff != "" {
						t.Fatalf("unexpected request message (-want +got):\n%s", diff)
					}

					if diff := cmp.Diff(unix.RTM_GETROUTE, int(family)); diff != "" {
						t.Fatalf("unexpected netlink header family (-want +got):\n%s", diff)
					}

					if diff := cmp.Diff(netlink.Request|netlink.Dump, flags); diff != "" {
						t.Fatalf("unexpected netlink header flags (-want +got):\n%s", diff)
					}

					return tt.msgs, nil
				},
			}

			var routes []Route
			panicked := panics(t, func() {
				out, err := a.LoopbackRoutes()
				if err != nil {
					t.Fatalf("failed to get routes: %v", err)
				}

				routes = out
			})
			if diff := cmp.Diff(tt.panicked, panicked); diff != "" {
				t.Fatalf("unexpected function panic (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.routes, routes, cmp.Comparer(ipPrefixEqual), cmp.Comparer(ipEqual)); diff != "" {
				t.Fatalf("unexpected routes (-want +got):\n%s", diff)
			}
		})
	}
}

func ipEqual(x, y netaddr.IP) bool             { return x == y }
func ipPrefixEqual(x, y netaddr.IPPrefix) bool { return x == y }

func panics(t *testing.T, fn func()) (panicked bool) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Logf("panic: %v", r)
			panicked = true
		}
	}()

	fn()
	return false
}
