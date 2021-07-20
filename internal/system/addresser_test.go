// Copyright 2021 Matt Layher
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

package system_test

import (
	"net"
	"strings"
	"testing"

	"github.com/mdlayher/corerad/internal/system"
	"inet.af/netaddr"
)

func TestIntegrationAddresser(t *testing.T) {
	ifis, err := net.Interfaces()
	if err != nil {
		t.Fatalf("failed to get interfaces: %v", err)
	}

	if len(ifis) == 0 {
		// Probably impossible but better to explicitly skip.
		t.Skipf("skipping, found no network interfaces")
	}

	a := system.NewAddresser()
	for _, ifi := range ifis {
		ips, err := a.AddressesByIndex(ifi.Index)
		if err != nil {
			t.Fatalf("failed to get netaddr addresses for %q: %v", ifi.Name, err)
		}

		// For each interface, track all of the known IPv6 addresses to compare
		// against the stdlib's output.
		seen := make(map[netaddr.IPPrefix]bool)
		for _, ip := range ips {
			seen[ip.Address] = false

			// We can't verify Address flags using stdlib so just log the ones
			// that are set for informational purposes.
			var flags []string
			if ip.Deprecated {
				flags = append(flags, "deprecated")
			}
			if ip.StablePrivacy {
				flags = append(flags, "stable-privacy")
			}
			if ip.Temporary {
				flags = append(flags, "temporary")
			}
			if ip.Tentative {
				flags = append(flags, "tentative")
			}

			t.Logf("%s: %s, flags: [%s]", ifi.Name, ip.Address, strings.Join(flags, ", "))
		}

		addrs, err := ifi.Addrs()
		if err != nil {
			t.Fatalf("failed to get stdlib addresses for %q: %v", ifi.Name, err)
		}

		// Filter out any values which are not IPv6 *net.IPNets and also verify
		// that all addresses were found by the addresser.
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}

			ipp, ok := netaddr.FromStdIPNet(ipn)
			if !ok || !ipp.IP().Is6() {
				continue
			}

			if _, ok := seen[ipp]; !ok {
				t.Fatalf("stdlib found interface %q address %q, not found by addresser", ifi.Name, ipp)
			}

			seen[ipp] = true
		}

		// Finally verify that every prefix was found in both outputs.
		for addr, ok := range seen {
			if !ok {
				t.Fatalf("interface %q address %q was not found", ifi.Name, addr)
			}
		}
	}
}
