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

package system_test

import (
	"net"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/system"
	"golang.org/x/net/nettest"
	"inet.af/netaddr"
)

func TestIntegrationAddresserAddresses(t *testing.T) {
	ifis, err := net.Interfaces()
	if err != nil {
		t.Fatalf("failed to get interfaces: %v", err)
	}

	if len(ifis) == 0 {
		// Probably impossible but better to explicitly skip.
		t.Skipf("skipping, found no network interfaces")
	}

	// Compare the OS-specific implementation against the generic net one.
	var (
		sysA = system.NewAddresser()
		netA = system.NewNetAddresser()
	)

	for _, ifi := range ifis {
		gotIPs, err := sysA.AddressesByIndex(ifi.Index)
		if err != nil {
			t.Fatalf("failed to get system addresses for %q: %v", ifi.Name, err)
		}

		// For each interface, track all of the known IPv6 addresses to compare
		// against the stdlib's output.
		seen := make(map[netaddr.IPPrefix]bool)
		for _, ip := range gotIPs {
			seen[ip.Address] = false

			// We can't verify Address flags using stdlib so just log the ones
			// that are set for informational purposes.
			var flags []string
			if ip.Deprecated {
				flags = append(flags, "deprecated")
			}
			if ip.ManageTemporaryAddresses {
				flags = append(flags, "manage-temporary-addresses")
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

			t.Logf("%s: %s, flags: [%s], forever: %v",
				ifi.Name, ip.Address, strings.Join(flags, ", "), ip.ValidForever)
		}

		wantIPs, err := netA.AddressesByIndex(ifi.Index)
		if err != nil {
			t.Fatalf("failed to get stdlib addresses for %q: %v", ifi.Name, err)
		}

		// Verify that all addresses were found by the addresser.
		for _, ip := range wantIPs {
			// The netAddresser should always set all flags to false, so just
			// compare against zero values with the Address set.
			if diff := cmp.Diff(system.IP{Address: ip.Address}, ip, cmp.Comparer(ipPrefixEqual)); diff != "" {
				t.Fatalf("unexpected system IP from netAddresser (-want +got):\n%s", diff)
			}

			if _, ok := seen[ip.Address]; !ok {
				t.Fatalf("stdlib found interface %q address %q, not found by addresser", ifi.Name, ip.Address)
			}

			seen[ip.Address] = true
		}

		// Finally verify that every prefix was found in both outputs.
		for addr, ok := range seen {
			if !ok {
				t.Fatalf("interface %q address %q was not found", ifi.Name, addr)
			}
		}
	}
}

func TestIntegrationAddresserLoopbackRoutes(t *testing.T) {
	lo, err := nettest.LoopbackInterface()
	if err != nil {
		t.Skipf("skipping, found no loopback network interface: %v", err)
	}

	routes, err := system.NewAddresser().LoopbackRoutes()
	if err != nil {
		t.Fatalf("failed to get loopback routes: %v", err)
	}

	t.Logf("loopback: %s, index: %d, flags: %s", lo.Name, lo.Index, lo.Flags)
	for _, r := range routes {
		t.Logf("  - %s: index %d, preference: %s", r.Prefix, r.Index, r.Preference)
	}
}

func ipPrefixEqual(x, y netaddr.IPPrefix) bool { return x == y }
