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
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/mdlayher/ndp"
	"inet.af/netaddr"
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
	// Parameters from configuration.
	Prefix            netaddr.IPPrefix
	OnLink            bool
	Autonomous        bool
	ValidLifetime     time.Duration
	PreferredLifetime time.Duration

	// Whether or not this prefix will be treated as deprecated when the Prefix
	// is applied, and the time used to calculate the expiration time.
	Epoch      time.Time
	Deprecated bool

	// Functions which can be swapped for tests.
	TimeNow func() time.Time
	Addrs   func() ([]net.Addr, error)
}

// Name implements Plugin.
func (p *Prefix) Name() string { return "prefix" }

// String implements Plugin.
func (p *Prefix) String() string {
	prefix := p.Prefix.String()
	if p.wildcard() {
		// Make a best-effort to note the current prefixes if the user is using
		// the wildcard syntax. If this returns an error, we'll return "::/N"
		// with no further information.
		if ps, err := p.currentPrefixes(); err == nil {
			ss := make([]string, 0, len(ps))
			for _, p := range ps {
				ss = append(ss, p.String())
			}

			prefix = fmt.Sprintf("%s [%s]", prefix, strings.Join(ss, ", "))
		}
	}

	var flags []string

	// Note deprecated as a flag.
	if p.Deprecated {
		flags = append(flags, "DEPRECATED")
	}

	if p.OnLink {
		flags = append(flags, "on-link")
	}
	if p.Autonomous {
		flags = append(flags, "autonomous")
	}

	return fmt.Sprintf("%s [%s], preferred: %s, valid: %s",
		prefix,
		strings.Join(flags, ", "),
		durString(p.PreferredLifetime),
		durString(p.ValidLifetime),
	)
}

// Prepare implements Plugin.
func (p *Prefix) Prepare(ifi *net.Interface) error {
	// Use the real system time.
	p.TimeNow = time.Now

	// Fetch addresses from the specified interface whenever invoked.
	p.Addrs = ifi.Addrs
	return nil
}

// Apply implements Plugin.
func (p *Prefix) Apply(ra *ndp.RouterAdvertisement) error {
	if !p.wildcard() {
		// User specified an exact prefix so apply it directly.
		p.applyPrefixes([]netaddr.IPPrefix{p.Prefix}, ra)
		return nil
	}

	// User specified the ::/N wildcard syntax, fetch all of the current
	// prefixes on the interface.
	prefixes, err := p.currentPrefixes()
	if err != nil {
		return err
	}

	// Produce a PrefixInformation option for each configured prefix.
	// All prefixes expanded from ::/N have the same configuration.
	p.applyPrefixes(prefixes, ra)
	return nil
}

// wildcard determines if the prefix is configured with the ::/N wildcard
// syntax.
func (p *Prefix) wildcard() bool { return p.Prefix.IP() == netaddr.IPv6Unspecified() }

// currentPrefixes fetches the current prefix IPs from the interface.
func (p *Prefix) currentPrefixes() ([]netaddr.IPPrefix, error) {
	// Expand ::/N to all unique, non-link local prefixes with matching length
	// on this interface.
	addrs, err := p.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch IP addresses: %v", err)
	}

	var prefixes []netaddr.IPPrefix
	seen := make(map[netaddr.IPPrefix]struct{})
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}

		ipp, ok := netaddr.FromStdIPNet(ipn)
		if !ok {
			panicf("corerad: invalid net.IPNet: %+v", a)
		}

		// Only advertise non-link-local IPv6 prefixes that also have a
		// matching mask:
		// https://tools.ietf.org/html/rfc4861#section-4.6.2.
		if ipp.IP().Is4() || ipp.IP().IsLinkLocalUnicast() || ipp.Bits() != p.Prefix.Bits() {
			continue
		}

		// Found a match, mask and keep the prefix bits of the address, and only
		// add each prefix once.
		pfx := ipp.Masked()
		if _, ok := seen[pfx]; ok {
			continue
		}
		seen[pfx] = struct{}{}

		prefixes = append(prefixes, pfx)
	}

	// For output consistency.
	sort.SliceStable(prefixes, func(i, j int) bool {
		return prefixes[i].IP().Less(prefixes[j].IP())
	})

	return prefixes, nil
}

// applyPrefixes unpacks prefixes into ndp.PrefixInformation options within ra.
func (p *Prefix) applyPrefixes(prefixes []netaddr.IPPrefix, ra *ndp.RouterAdvertisement) {
	// Pre-allocate space for prefixes since we know how many are needed.
	opts := make([]ndp.Option, 0, len(prefixes))
	for _, pfx := range prefixes {
		valid, pref := p.lifetimes()

		opts = append(opts, &ndp.PrefixInformation{
			PrefixLength:                   pfx.Bits(),
			OnLink:                         p.OnLink,
			AutonomousAddressConfiguration: p.Autonomous,
			ValidLifetime:                  valid,
			PreferredLifetime:              pref,
			Prefix:                         pfx.IP().IPAddr().IP,
		})
	}

	ra.Options = append(ra.Options, opts...)
}

// lifetimes calculates a Prefix's lifetimes as either fixed values or dynamic
// ones when a Prefix is deprecated.
func (p *Prefix) lifetimes() (valid, pref time.Duration) {
	if !p.Deprecated {
		return p.ValidLifetime, p.PreferredLifetime
	}

	if p.Epoch.IsZero() {
		panic("plugin: cannot calculate deprecated Prefix lifetimes with zero epoch")
	}

	now := p.TimeNow()

	var (
		validT = p.Epoch.Add(p.ValidLifetime)
		prefT  = p.Epoch.Add(p.PreferredLifetime)
	)

	if now.Equal(validT) || now.After(validT) {
		valid = 0
	} else {
		valid = validT.Sub(now)
	}

	if now.Equal(prefT) || now.After(prefT) {
		pref = 0
	} else {
		pref = prefT.Sub(now)
	}

	return valid, pref
}

// A Route configures a NDP Route Information option.
type Route struct {
	// Parameters from configuration.
	Prefix     netaddr.IPPrefix
	Preference ndp.Preference
	Lifetime   time.Duration

	// Whether or not this route will be treated as deprecated when the Route
	// is applied, and the time used to calculate the expiration time.
	Epoch      time.Time
	Deprecated bool

	// Functions which can be swapped for tests.
	TimeNow func() time.Time
}

// Name implements Plugin.
func (*Route) Name() string { return "route" }

// String implements Plugin.
func (r *Route) String() string {
	// Note deprecation similar to Prefix if applicable.
	var deprecated string
	if r.Deprecated {
		deprecated = " [DEPRECATED]"
	}

	return fmt.Sprintf("%s%s, preference: %s, lifetime: %s",
		r.Prefix,
		deprecated,
		r.Preference.String(),
		durString(r.Lifetime),
	)
}

// Prepare implements Plugin.
func (r *Route) Prepare(_ *net.Interface) error {
	// Use the real system time.
	r.TimeNow = time.Now

	return nil
}

// Apply implements Plugin.
func (r *Route) Apply(ra *ndp.RouterAdvertisement) error {
	ra.Options = append(ra.Options, &ndp.RouteInformation{
		PrefixLength:  r.Prefix.Bits(),
		Preference:    r.Preference,
		RouteLifetime: r.lifetime(),
		Prefix:        r.Prefix.IP().IPAddr().IP,
	})

	return nil
}

// lifetimes calculates a Route's lifetime as either a fixed or dynamic value
// when a Route is deprecated.
func (r *Route) lifetime() time.Duration {
	if !r.Deprecated {
		return r.Lifetime
	}

	if r.Epoch.IsZero() {
		panic("plugin: cannot calculate deprecated Route lifetimes with zero epoch")
	}

	now := r.TimeNow()
	lt := r.Epoch.Add(r.Lifetime)

	if now.Equal(lt) || now.After(lt) {
		return 0
	}

	return lt.Sub(now)
}

// RDNSS configures a NDP Recursive DNS Servers option.
type RDNSS struct {
	// Parameters from configuration.
	Lifetime time.Duration
	Servers  []netaddr.IP

	// Functions which can be swapped for tests.
	Addrs func() ([]net.Addr, error)
}

// Name implements Plugin.
func (r *RDNSS) Name() string { return "rdnss" }

// String implements Plugin.
func (r *RDNSS) String() string {
	ips := make([]string, 0, len(r.Servers))
	for _, s := range r.Servers {
		ips = append(ips, s.String())
	}

	servers := fmt.Sprintf("[%s]", strings.Join(ips, ", "))
	if r.wildcard() {
		// Make a best-effort to note the current server if the user is using
		// the wildcard syntax. If this returns an error, we'll return "::"
		// with no further information.
		if s, err := r.currentServer(); err == nil {
			servers = fmt.Sprintf(":: [%s]", s.String())
		}
	}

	return fmt.Sprintf("servers: %s, lifetime: %s", servers, durString(r.Lifetime))
}

// Prepare implements Plugin.
func (r *RDNSS) Prepare(ifi *net.Interface) error {
	// Fetch addresses from the specified interface whenever invoked.
	r.Addrs = ifi.Addrs
	return nil
}

// Apply implements Plugin.
func (r *RDNSS) Apply(ra *ndp.RouterAdvertisement) error {
	if !r.wildcard() {
		// User specified exact servers so apply them directly.
		r.applyServers(r.Servers, ra)
		return nil
	}

	// User specified the :: wildcard syntax, automatically choose a DNS server
	// address from this interface.
	server, err := r.currentServer()
	if err != nil {
		return err
	}

	// Produce a RecursiveDNSServers option for this server.
	r.applyServers([]netaddr.IP{server}, ra)
	return nil
}

// applyServers unpacks servers into an ndp.RecursiveDNSServer option within ra.
func (r *RDNSS) applyServers(servers []netaddr.IP, ra *ndp.RouterAdvertisement) {
	ips := make([]net.IP, 0, len(servers))
	for _, s := range servers {
		ips = append(ips, s.IPAddr().IP)
	}

	ra.Options = append(ra.Options, &ndp.RecursiveDNSServer{
		Lifetime: r.Lifetime,
		Servers:  ips,
	})
}

// wildcard determines if the RDNSS option is configured with the :: wildcard
// syntax.
func (r *RDNSS) wildcard() bool {
	// TODO(mdlayher): allow both wildcard and non-wildcard servers?
	return len(r.Servers) == 1 && r.Servers[0] == netaddr.IPv6Unspecified()
}

// currentServer fetches the current DNS server IP from the interface.
func (r *RDNSS) currentServer() (netaddr.IP, error) {
	// Expand :: to one of the IPv6 addresses on this interface.
	addrs, err := r.Addrs()
	if err != nil {
		return netaddr.IP{}, fmt.Errorf("failed to fetch IP addresses: %v", err)
	}

	var ips []netaddr.IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}

		ipp, ok := netaddr.FromStdIPNet(ipn)
		if !ok {
			panicf("corerad: invalid net.IPNet: %+v", a)
		}

		// Only consider IPv6 addresses.
		if ipp.IP().Is4() {
			continue
		}

		ips = append(ips, ipp.IP())
	}

	switch len(ips) {
	case 0:
		// No IPv6 addresses, cannot use wildcard syntax.
		return netaddr.IP{}, errors.New("interface has no IPv6 addresses")
	case 1:
		// One IPv6 address, use that one.
		return ips[0], nil
	}

	// More than one IPv6 address was found. Now that we've gathered the
	// addresses on this interface, select one as follows:
	//
	// 1) Unique Local Address (ULA)
	//   - if assigned, high probability of use for internal-only services.
	// 2) Global Unicast Address (GUA)
	//   - de-facto choice when ULA is not available.
	// 3) Link-Local Address (LLA)
	//   - last resort, doesn't work across subnets but since this machine is
	//     also running CoreRAD that may not be a problem.
	//
	// In the event of a tie, the lesser address by byte comparison wins.
	//
	// TODO(mdlayher): actually consider OS-specific data like
	// temporary/deprecated address flags.
	//
	// TODO(mdlayher): infer permanence of an address from EUI-64 format.
	sort.SliceStable(ips, func(i, j int) bool {
		// Prefer ULA.
		if isI, isJ := isULA(ips[i]), isULA(ips[j]); isI && !isJ {
			return true
		} else if isJ && !isI {
			return false
		}

		// Prefer GUA.
		if isI, isJ := isGUA(ips[i]), isGUA(ips[j]); isI && !isJ {
			return true
		} else if isJ && !isI {
			return false
		}

		// Prefer LLA.
		if isI, isJ := ips[i].IsLinkLocalUnicast(), ips[j].IsLinkLocalUnicast(); isI && !isJ {
			return true
		} else if isJ && !isI {
			return false
		}

		// Tie-breaker: prefer lowest address.
		return ips[i].Less(ips[j])
	})

	// The first address wins.
	return ips[0], nil
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

// TODO(mdlayher): upstream into inet.af/netaddr.

func isGUA(ip netaddr.IP) bool { return ip.IPAddr().IP.IsGlobalUnicast() }

var ula = netaddr.MustParseIPPrefix("fc00::/7")

func isULA(ip netaddr.IP) bool { return ula.Contains(ip) }

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
