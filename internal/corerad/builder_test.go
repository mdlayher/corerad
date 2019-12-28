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

package corerad

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/ndp"
)

func Test_builderBuild(t *testing.T) {
	tests := []struct {
		name string
		b    builder
		ifi  config.Interface
		ra   *ndp.RouterAdvertisement
	}{
		{
			name: "no config",
			ra:   &ndp.RouterAdvertisement{},
		},
		{
			name: "static prefix",
			ifi: config.Interface{
				Plugins: []config.Plugin{
					&config.Prefix{
						Prefix:            mustCIDR("2001:db8::/32"),
						OnLink:            true,
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
					},
				},
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
		},
		{
			name: "automatic prefixes",
			b: builder{
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
			ifi: config.Interface{
				Plugins: []config.Plugin{
					&config.Prefix{
						Prefix:            mustCIDR("::/64"),
						OnLink:            true,
						Autonomous:        true,
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
					},
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ra, err := tt.b.Build(tt.ifi)
			if err != nil {
				t.Fatalf("failed to build RA: %v", err)
			}

			if diff := cmp.Diff(tt.ra, ra); diff != "" {
				t.Fatalf("unexpected RA (-want +got):\n%s", diff)
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
