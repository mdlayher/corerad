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
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/mdlayher/ndp"
)

// A Plugin specifies a CoreRAD plugin's configuration.
type Plugin interface {
	// Name is the string name of the plugin.
	Name() string

	// String is the string representation of the plugin's configuration.
	String() string

	// Prepare prepares a Plugin for use with the specified network interface.
	Prepare(ifi *net.Interface) error

	// Apply applies Plugin data to the input RA.
	Apply(ra *ndp.RouterAdvertisement) error
}

// DNSSL configures a NDP DNS Search List option.
type DNSSL struct {
	Lifetime    time.Duration
	DomainNames []string
}

// Name implements Plugin.
func (d *DNSSL) Name() string { return "dnssl" }

// String implements Plugin.
func (d *DNSSL) String() string {
	return fmt.Sprintf("domain names: [%s], lifetime: %s",
		strings.Join(d.DomainNames, ", "), durString(d.Lifetime))
}

// Prepare implements Plugin.
func (*DNSSL) Prepare(_ *net.Interface) error { return nil }

// Apply implements Plugin.
func (d *DNSSL) Apply(ra *ndp.RouterAdvertisement) error {
	ra.Options = append(ra.Options, &ndp.DNSSearchList{
		Lifetime:    d.Lifetime,
		DomainNames: d.DomainNames,
	})

	return nil
}

// LLA configures a NDP Source Link Layer Address option.
type LLA net.HardwareAddr

// Name implements Plugin.
func (l *LLA) Name() string { return "lla" }

// String implements Plugin.
func (l *LLA) String() string {
	return fmt.Sprintf("source link-layer address: %s", net.HardwareAddr(*l))
}

// Prepare implements Plugin.
func (l *LLA) Prepare(ifi *net.Interface) error {
	*l = LLA(ifi.HardwareAddr)
	return nil
}

// Apply implements Plugin.
func (l *LLA) Apply(ra *ndp.RouterAdvertisement) error {
	ra.Options = append(ra.Options, &ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      net.HardwareAddr(*l),
	})

	return nil
}

// MTU configures a NDP MTU option.
type MTU int

// NewMTU creates a MTU from an integer.
func NewMTU(mtu int) *MTU {
	m := MTU(mtu)
	return &m
}

// Name implements Plugin.
func (m *MTU) Name() string { return "mtu" }

// String implements Plugin.
func (m *MTU) String() string { return fmt.Sprintf("MTU: %d", *m) }

// Prepare implements Plugin.
func (*MTU) Prepare(_ *net.Interface) error { return nil }

// Apply implements Plugin.
func (m *MTU) Apply(ra *ndp.RouterAdvertisement) error {
	ra.Options = append(ra.Options, ndp.NewMTU(uint32(*m)))
	return nil
}

// A Prefix configures a NDP Prefix Information option.
type Prefix struct {
	Prefix            *net.IPNet
	OnLink            bool
	Autonomous        bool
	ValidLifetime     time.Duration
	PreferredLifetime time.Duration
	Addrs             func() ([]net.Addr, error)
}

// Name implements Plugin.
func (p *Prefix) Name() string { return "prefix" }

// String implements Plugin.
func (p *Prefix) String() string {
	var flags []string
	if p.OnLink {
		flags = append(flags, "on-link")
	}
	if p.Autonomous {
		flags = append(flags, "autonomous")
	}

	return fmt.Sprintf("%s [%s], preferred: %s, valid: %s",
		p.Prefix,
		strings.Join(flags, ", "),
		durString(p.PreferredLifetime),
		durString(p.ValidLifetime),
	)
}

// Prepare implements Plugin.
func (p *Prefix) Prepare(ifi *net.Interface) error {
	// Fetch addresses from the specified interface whenever invoked.
	p.Addrs = ifi.Addrs
	return nil
}

// Apply implements Plugin.
func (p *Prefix) Apply(ra *ndp.RouterAdvertisement) error {
	length, _ := p.Prefix.Mask.Size()

	var prefixes []net.IP
	if p.Prefix.IP.Equal(net.IPv6zero) {
		// Expand ::/N to all unique, non-link local prefixes with matching
		// length on this interface.
		addrs, err := p.Addrs()
		if err != nil {
			return fmt.Errorf("failed to fetch IP addresses: %v", err)
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
	for _, pfx := range prefixes {
		ra.Options = append(ra.Options, &ndp.PrefixInformation{
			PrefixLength:                   uint8(length),
			OnLink:                         p.OnLink,
			AutonomousAddressConfiguration: p.Autonomous,
			ValidLifetime:                  p.ValidLifetime,
			PreferredLifetime:              p.PreferredLifetime,
			Prefix:                         pfx,
		})
	}

	return nil
}

// A Route configures a NDP Route Information option.
type Route struct {
	Prefix     *net.IPNet
	Preference ndp.Preference
	Lifetime   time.Duration
}

// Name implements Plugin.
func (*Route) Name() string { return "route" }

// String implements Plugin.
func (r *Route) String() string {
	return fmt.Sprintf("%s, preference: %s, lifetime: %s",
		r.Prefix,
		r.Preference.String(),
		durString(r.Lifetime),
	)
}

// Prepare implements Plugin.
func (*Route) Prepare(_ *net.Interface) error { return nil }

// Apply implements Plugin.
func (r *Route) Apply(ra *ndp.RouterAdvertisement) error {
	length, _ := r.Prefix.Mask.Size()

	ra.Options = append(ra.Options, &ndp.RouteInformation{
		PrefixLength:  uint8(length),
		Preference:    r.Preference,
		RouteLifetime: r.Lifetime,
		Prefix:        r.Prefix.IP,
	})

	return nil
}

// RDNSS configures a NDP Recursive DNS Servers option.
type RDNSS struct {
	Lifetime time.Duration
	Servers  []net.IP
}

// Name implements Plugin.
func (r *RDNSS) Name() string { return "rdnss" }

// String implements Plugin.
func (r *RDNSS) String() string {
	ips := make([]string, 0, len(r.Servers))
	for _, s := range r.Servers {
		ips = append(ips, s.String())
	}

	return fmt.Sprintf("servers: [%s], lifetime: %s",
		strings.Join(ips, ", "), durString(r.Lifetime))
}

// Prepare implements Plugin.
func (*RDNSS) Prepare(_ *net.Interface) error { return nil }

// Apply implements Plugin.
func (r *RDNSS) Apply(ra *ndp.RouterAdvertisement) error {
	ra.Options = append(ra.Options, &ndp.RecursiveDNSServer{
		Lifetime: r.Lifetime,
		Servers:  r.Servers,
	})

	return nil
}

// durString converts a time.Duration into a string while also recognizing
// certain CoreRAD sentinel values.
func durString(d time.Duration) string {
	switch d {
	case ndp.Infinity:
		return "infinite"
	default:
		return d.String()
	}
}
