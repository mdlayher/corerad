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

	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/ndp"
)

// A builder builds router advertisement messages from configuration.
type builder struct {
	// Addrs is a swappable function which produces IP addresses for an interface.
	Addrs func() ([]net.Addr, error)
}

// Build creates a router advertisement from configuration.
func (b *builder) Build(ifi config.Interface) (*ndp.RouterAdvertisement, error) {
	ra := &ndp.RouterAdvertisement{
		ManagedConfiguration: ifi.Managed,
		OtherConfiguration:   ifi.OtherConfig,
		RouterLifetime:       ifi.DefaultLifetime,
	}

	for _, p := range ifi.Plugins {
		switch p := p.(type) {
		case *config.DNSSL:
			ra.Options = append(ra.Options, &ndp.DNSSearchList{
				Lifetime:    p.Lifetime,
				DomainNames: p.DomainNames,
			})
		case *config.MTU:
			ra.Options = append(ra.Options, ndp.NewMTU(uint32(*p)))
		case *config.Prefix:
			opts, err := b.prefixInformation(p)
			if err != nil {
				return nil, err
			}

			ra.Options = append(ra.Options, opts...)
		case *config.RDNSS:
			ra.Options = append(ra.Options, &ndp.RecursiveDNSServer{
				Lifetime: p.Lifetime,
				Servers:  p.Servers,
			})
		}
	}

	return ra, nil
}

// prefixInformation produces ndp.PrefixInformation options for the prefix plugin.
func (b *builder) prefixInformation(p *config.Prefix) ([]ndp.Option, error) {
	length, _ := p.Prefix.Mask.Size()

	var prefixes []net.IP
	if p.Prefix.IP.Equal(net.IPv6zero) {
		// Expand ::/N to all unique, non-link local prefixes with matching
		// length on this interface.
		addrs, err := b.Addrs()
		if err != nil {
			return nil, fmt.Errorf("failed to fetch IP addresses: %v", err)
		}

		seen := make(map[string]struct{})
		for _, a := range addrs {
			// Only advertise non-link-local prefixes:
			// https://tools.ietf.org/html/rfc4861#section-4.6.2.
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() {
				continue
			}

			size, _ := ipn.Mask.Size()
			if size != length {
				continue
			}

			// Found a match, mask and keep the prefix bits of the address.
			ip := ipn.IP.Mask(ipn.Mask)

			// Only add each prefix once.
			if _, ok := seen[ip.String()]; ok {
				continue
			}
			seen[ip.String()] = struct{}{}

			prefixes = append(prefixes, ip)
		}
	} else {
		// Use the specified prefix.
		prefixes = append(prefixes, p.Prefix.IP)
	}

	// Produce a PrefixInformation option for each configured prefix.
	// All prefixes expanded from ::/N have the same configuration.
	opts := make([]ndp.Option, 0, len(prefixes))
	for _, pfx := range prefixes {
		opts = append(opts, &ndp.PrefixInformation{
			PrefixLength:                   uint8(length),
			OnLink:                         p.OnLink,
			AutonomousAddressConfiguration: p.Autonomous,
			ValidLifetime:                  p.ValidLifetime,
			PreferredLifetime:              p.PreferredLifetime,
			Prefix:                         pfx,
		})
	}

	return opts, nil
}
