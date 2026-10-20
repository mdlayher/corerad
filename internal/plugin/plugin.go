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
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/mdlayher/corerad/internal/system"
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

// CaptivePortal configures a NDP Captive Portal option.
type CaptivePortal struct {
	Portal *ndp.CaptivePortal
}

// NewCaptivePortal creates a CaptivePortal from a string.
func NewCaptivePortal(uri string) (*CaptivePortal, error) {
	cp, err := ndp.NewCaptivePortal(uri)
	if err != nil {
		return nil, err
	}

	return &CaptivePortal{Portal: cp}, nil
}

// UnrestrictedPortal creates a CaptivePortal which advertises itself as
// unrestricted.
func UnrestrictedPortal() *CaptivePortal {
	return &CaptivePortal{
		Portal: &ndp.CaptivePortal{URI: ndp.Unrestricted},
	}
}

// Name implements Plugin.
func (*CaptivePortal) Name() string { return "captive-portal" }

// String implements Plugin.
func (cp *CaptivePortal) String() string { return fmt.Sprintf("URI: %q", cp.Portal.URI) }

// Prepare implements Plugin.
func (*CaptivePortal) Prepare(_ *net.Interface) error { return nil }

// Apply implements Plugin.
func (cp *CaptivePortal) Apply(ra *ndp.RouterAdvertisement) error {
	ra.Options = append(ra.Options, cp.Portal)
	return nil
}

// PREF64 configures a NDP PREF64 option, used to communicate prefixes to
// clients for NAT64.
type PREF64 struct {
	Inner *ndp.PREF64
}

// Constant derived from https://datatracker.ietf.org/doc/html/rfc8781#section-4.1.
const maxPref64Lifetime = 8191 * 8 * time.Second

// NewPREF64 computes PREF64 plugin values given a prefix and router
// advertisement max interval value.
func NewPREF64(prefix netip.Prefix, maxInterval time.Duration) *PREF64 {
	// Calculate the scaled lifetime using MaxRtrAdvInterval.
	// See https://datatracker.ietf.org/doc/html/rfc8781#section-4.1-2
	lifetime := maxPref64Lifetime
	if int(maxInterval.Seconds())*3 < int(lifetime.Seconds()) {
		lifetimeSeconds := int(maxInterval.Seconds()) * 3
		if r := int(lifetimeSeconds) % 8; r > 0 {
			lifetimeSeconds += 8 - r
		}

		lifetime = time.Duration(lifetimeSeconds) * time.Second
	}

	return &PREF64{
		Inner: &ndp.PREF64{
			Prefix:   prefix,
			Lifetime: lifetime,
		},
	}
}

// Name implements Plugin.
func (*PREF64) Name() string { return "pref64" }

// String implements Plugin.
func (p *PREF64) String() string {
	return fmt.Sprintf("%s, lifetime: %s", p.Inner.Prefix, p.Inner.Lifetime)
}

// Prepare implements Plugin.
func (*PREF64) Prepare(_ *net.Interface) error { return nil }

// Apply implements Plugin.
func (p *PREF64) Apply(ra *ndp.RouterAdvertisement) error {
	ra.Options = append(ra.Options, p.Inner)
	return nil
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
type LLA struct {
	Addr net.HardwareAddr
}

// Name implements Plugin.
func (l *LLA) Name() string { return "lla" }

// String implements Plugin.
func (l *LLA) String() string {
	var s string
	if l.Addr != nil {
		s = l.Addr.String()
	} else {
		s = "n/a"
	}

	return fmt.Sprintf("source link-layer address: %s", s)
}

// Prepare implements Plugin.
func (l *LLA) Prepare(ifi *net.Interface) error {
	l.Addr = ifi.HardwareAddr
	return nil
}

// Apply implements Plugin.
func (l *LLA) Apply(ra *ndp.RouterAdvertisement) error {
	// Only apply the option if Addr is set. It would not be set for
	// point-to-point links, for example.
	if l.Addr == nil {
		return nil
	}

	ra.Options = append(ra.Options, &ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      l.Addr,
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
	// Whether or not this Prefix should automatically infer and apply the
	// appropriate IPv6 prefixes to the configuration.
	Auto bool

	// Parameters from configuration.
	Prefix            netip.Prefix
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
	Addrs   func() ([]system.IP, error)
}

// Name implements Plugin.
func (p *Prefix) Name() string { return "prefix" }

// String implements Plugin.
func (p *Prefix) String() string {
	prefix := p.Prefix.String()
	if p.Auto {
		// Make a best-effort to note the current prefixes if the user is using
		// the wildcard syntax. If this returns an error, we'll return "::/N"
		// with no further information.
		if ps, err := p.current(); err == nil {
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
	a := system.NewAddresser()
	p.Addrs = func() ([]system.IP, error) { return a.AddressesByIndex(ifi.Index) }

	return nil
}

// Apply implements Plugin.
func (p *Prefix) Apply(ra *ndp.RouterAdvertisement) error {
	if !p.Auto {
		// User specified an exact prefix so apply it directly.
		p.apply([]netip.Prefix{p.Prefix}, ra)
		return nil
	}

	// User specified the ::/N wildcard syntax, fetch all of the current
	// prefixes on the interface.
	prefixes, err := p.current()
	if err != nil {
		return err
	}

	// Produce a PrefixInformation option for each configured prefix.
	// All prefixes expanded from ::/N have the same configuration.
	p.apply(prefixes, ra)
	return nil
}

// current fetches the current prefix IPs from the interface.
func (p *Prefix) current() ([]netip.Prefix, error) {
	// Expand ::/N to all unique, non-link local prefixes with matching length
	// on this interface.
	addrs, err := p.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch IP addresses: %v", err)
	}

	var prefixes []netip.Prefix
	seen := make(map[netip.Prefix]struct{})
	for _, a := range addrs {
		// Only advertise prefixes derived from usable addresses that also have
		// a matching mask:
		// https://tools.ietf.org/html/rfc4861#section-4.6.2.
		if !usableAddr(a) || a.Address.Bits() != p.Prefix.Bits() {
			continue
		}

		// TODO(mdlayher): handle a.Deprecated by also flagging p.Deprecated?

		// Found a match, mask and keep the prefix bits of the address, and only
		// add each prefix once.
		pfx := a.Address.Masked()
		if _, ok := seen[pfx]; ok {
			continue
		}
		seen[pfx] = struct{}{}

		prefixes = append(prefixes, pfx)
	}

	// For output consistency.
	slices.SortStableFunc(prefixes, func(a, b netip.Prefix) int {
		return a.Addr().Compare(b.Addr())
	})

	return prefixes, nil
}

// usableAddr reports whether a is an interface address which may be used to
// derive automatic Prefix and Route options.
func usableAddr(a system.IP) bool {
	// Only consider non-link-local IPv6 addresses.
	ip := a.Address.Addr()
	if ip.Is4() || ip.IsLinkLocalUnicast() {
		return false
	}

	// Don't consider addresses which are:
	//  - temporary: short-lived, used for outbound connections
	//  - tentative: may be awaiting duplicate address detection results
	return !a.Temporary && !a.Tentative
}

// apply unpacks prefixes into ndp.PrefixInformation options within ra.
func (p *Prefix) apply(prefixes []netip.Prefix, ra *ndp.RouterAdvertisement) {
	var (
		// Lifetime is the same for each prefix..
		valid, pref = p.lifetimes()

		// Pre-allocate space for prefixes since we know how many are needed.
		opts = make([]ndp.Option, 0, len(prefixes))
	)

	for _, pfx := range prefixes {
		opts = append(opts, &ndp.PrefixInformation{
			PrefixLength:                   uint8(pfx.Bits()),
			OnLink:                         p.OnLink,
			AutonomousAddressConfiguration: p.Autonomous,
			ValidLifetime:                  valid,
			PreferredLifetime:              pref,
			Prefix:                         pfx.Addr(),
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
	// Whether or not this Route should automatically infer and apply the
	// appropriate IPv6 loopback interface routes to the configuration.
	Auto bool

	// Parameters from configuration.
	Prefix     netip.Prefix
	Preference ndp.Preference
	Lifetime   time.Duration

	// Whether or not this route will be treated as deprecated when the Route
	// is applied, and the time used to calculate the expiration time.
	Epoch      time.Time
	Deprecated bool

	// Functions which can be swapped for tests.
	TimeNow func() time.Time
	Routes  func() ([]system.Route, error)
	Addrs   func() ([]system.IP, error)
}

// Name implements Plugin.
func (*Route) Name() string { return "route" }

// String implements Plugin.
func (r *Route) String() string {
	prefix := r.Prefix.String()
	if r.Auto {
		// Make a best-effort to note the current routes if the user is using
		// the wildcard syntax. If this returns an error, we'll return "::/N"
		// with no further information.
		if rs, err := r.current(); err == nil {
			ss := make([]string, 0, len(rs))
			for _, r := range rs {
				ss = append(ss, r.String())
			}

			prefix = fmt.Sprintf("%s [%s]", prefix, strings.Join(ss, ", "))
		}
	}

	// Note deprecation similar to Prefix if applicable.
	var deprecated string
	if r.Deprecated {
		deprecated = " [DEPRECATED]"
	}

	return fmt.Sprintf("%s%s, preference: %s, lifetime: %s",
		prefix,
		deprecated,
		r.Preference.String(),
		durString(r.Lifetime),
	)
}

// Prepare implements Plugin.
func (r *Route) Prepare(ifi *net.Interface) error {
	// Use the real system time.
	r.TimeNow = time.Now

	// Fetch automatic routes from loopback interfaces when available, and
	// addresses from the specified interface to filter those routes.
	a := system.NewAddresser()
	r.Routes = a.LoopbackRoutes
	r.Addrs = func() ([]system.IP, error) { return a.AddressesByIndex(ifi.Index) }

	return nil
}

// Apply implements Plugin.
func (r *Route) Apply(ra *ndp.RouterAdvertisement) error {
	if !r.Auto {
		// User specified an exact route so apply it directly.
		r.apply([]netip.Prefix{r.Prefix}, ra)
		return nil
	}

	// User specified the ::/N wildcard syntax, fetch all of the current routes
	// on the loopback interface.
	//
	// TODO(mdlayher): propagate preference from rtnetlink.
	routes, err := r.current()
	if err != nil {
		return err
	}

	// Produce a RouteInformation option for each configured loopback route. All
	// routes expanded from ::/N have the same configuration.
	r.apply(routes, ra)
	return nil
}

// current fetches the current IPv6 loopback routes from the system which
// cover one of the interface's own addresses.
//
// If either Routes or Addrs is unset (Prepare has not been called), current
// returns an error rather than panicking, so String omits the expanded routes.
func (r *Route) current() ([]netip.Prefix, error) {
	if r.Routes == nil || r.Addrs == nil {
		return nil, errors.New("plugin: route has not been prepared")
	}

	// Expand ::/N to all loopback routes.
	//
	// TODO(mdlayher): if we choose to accept syntax other than ::/0, we'll have
	// to update this logic.
	routes, err := r.Routes()
	if err != nil {
		return nil, err
	}

	addrs, err := r.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch IP addresses: %v", err)
	}

	// Only advertise loopback routes which cover one of this interface's
	// usable addresses, such as a prefix delegated to this router which is
	// also assigned to this link, as described in RFC 7084 L-3. Other routes
	// on loopback (blackhole discard prefixes, unrelated aggregates, etc.)
	// must be configured explicitly.
	prefixes := uncoveredPrefixes(coveringRoutes(routes, addrs))

	// For output consistency.
	slices.SortStableFunc(prefixes, func(a, b netip.Prefix) int {
		return a.Addr().Compare(b.Addr())
	})

	return prefixes, nil
}

// coveringRoutes returns the unique IPv6 prefixes from routes which cover one
// of the usable addresses in addrs, skipping /128s.
func coveringRoutes(routes []system.Route, addrs []system.IP) []netip.Prefix {
	var (
		prefixes []netip.Prefix
		seen     = make(map[netip.Prefix]struct{})
	)

	for _, rt := range routes {
		pfx := rt.Prefix
		if pfx.Addr().Is4() || pfx.IsSingleIP() {
			continue
		}

		if _, ok := seen[pfx]; ok {
			continue
		}

		if !coversUsableAddr(pfx, addrs) {
			continue
		}

		seen[pfx] = struct{}{}
		prefixes = append(prefixes, pfx)
	}

	return prefixes
}

// coversUsableAddr reports whether prefix contains any usable address in addrs.
func coversUsableAddr(prefix netip.Prefix, addrs []system.IP) bool {
	return slices.ContainsFunc(addrs, func(a system.IP) bool {
		return usableAddr(a) && prefix.Contains(a.Address.Addr())
	})
}

// uncoveredPrefixes returns the prefixes which are not covered by a strictly
// shorter prefix in the same set.
func uncoveredPrefixes(prefixes []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, pfx := range prefixes {
		if !isCovered(pfx, prefixes) {
			out = append(out, pfx)
		}
	}

	return out
}

// isCovered reports whether prefix is covered by a strictly shorter prefix in
// prefixes.
func isCovered(prefix netip.Prefix, prefixes []netip.Prefix) bool {
	return slices.ContainsFunc(prefixes, func(p netip.Prefix) bool {
		return p.Bits() < prefix.Bits() && p.Contains(prefix.Addr())
	})
}

// apply unpacks routes into ndp.PrefixInformation options within ra.
func (r *Route) apply(routes []netip.Prefix, ra *ndp.RouterAdvertisement) {
	var (
		// Lifetime is the same for each route.
		lt = r.lifetime()

		// Pre-allocate space for routes since we know how many are needed.
		opts = make([]ndp.Option, 0, len(routes))
	)

	for _, rt := range routes {
		ra.Options = append(ra.Options, &ndp.RouteInformation{
			PrefixLength:  uint8(rt.Bits()),
			Preference:    r.Preference,
			RouteLifetime: lt,
			Prefix:        rt.Addr(),
		})
	}

	ra.Options = append(ra.Options, opts...)
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
	// Whether or not this RDNSS should automatically infer and apply the
	// appropriate IPv6 DNS server address to the configuration.
	Auto bool

	// Parameters from configuration.
	Lifetime time.Duration
	Servers  []netip.Addr

	// Functions which can be swapped for tests.
	Addrs func() ([]system.IP, error)
}

// Name implements Plugin.
func (r *RDNSS) Name() string { return "rdnss" }

// String implements Plugin.
func (r *RDNSS) String() string {
	var servers []string
	if r.Auto {
		// Make a best-effort to note the current server if the user is using
		// the wildcard syntax. If this returns an error, we'll return "::"
		// with no further information.
		if s, err := r.current(); err == nil {
			servers = append(servers, fmt.Sprintf(":: [%s]", s.String()))
		} else {
			servers = append(servers, "::")
		}
	}

	for _, s := range r.Servers {
		if s.IsUnspecified() {
			// Remove unspecified addresses as they are stringified in the
			// r.Auto check.
			continue
		}

		servers = append(servers, s.String())
	}

	return fmt.Sprintf("servers: [%s], lifetime: %s", strings.Join(servers, ", "), durString(r.Lifetime))
}

// Prepare implements Plugin.
func (r *RDNSS) Prepare(ifi *net.Interface) error {
	// Fetch addresses from the specified interface whenever invoked.
	a := system.NewAddresser()
	r.Addrs = func() ([]system.IP, error) { return a.AddressesByIndex(ifi.Index) }

	return nil
}

// Apply implements Plugin.
func (r *RDNSS) Apply(ra *ndp.RouterAdvertisement) error {
	if !r.Auto {
		// User specified exact servers so apply them directly.
		r.apply(r.Servers, ra)
		return nil
	}

	// User specified the :: wildcard syntax, automatically choose a DNS server
	// address from this interface and prepend it to the list.
	server, err := r.current()
	if err != nil {
		return err
	}

	// Produce a RecursiveDNSServers option for this server.
	r.apply(append([]netip.Addr{server}, r.Servers...), ra)
	return nil
}

// apply unpacks servers into an ndp.RecursiveDNSServer option within ra.
func (r *RDNSS) apply(servers []netip.Addr, ra *ndp.RouterAdvertisement) {
	ra.Options = append(ra.Options, &ndp.RecursiveDNSServer{
		Lifetime: r.Lifetime,
		Servers:  servers,
	})
}

// current fetches the current DNS server IP from the interface.
func (r *RDNSS) current() (netip.Addr, error) {
	// Expand :: to one of the IPv6 addresses on this interface. The "best"
	// address will be chosen by comparing all addresses on the interface for
	// desired properties.
	addrs, err := r.Addrs()
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to fetch IP addresses: %v", err)
	}

	var best system.IP
	for _, a := range addrs {
		// Only consider IPv6 addresses which are not:
		//  - deprecated: should not be used when possible
		//  - temporary: short-lived, used for outbound connections
		//  - tentative: may be awaiting duplicate address detection results
		ip := a.Address.Addr()
		if ip.Is4() || a.Deprecated || a.Temporary || a.Tentative {
			continue
		}

		// Is this address better than our current best?
		best = betterRDNSS(best, a)
	}

	ip := best.Address.Addr()
	if !ip.IsValid() {
		// No usable IPv6 addresses, cannot use wildcard syntax.
		return netip.Addr{}, errors.New("interface has no usable IPv6 addresses")
	}

	return ip, nil
}

// betterRDNSS compares two IPv6 addresses and their metadata and returns the
// address which is more favorable of the two for use as an automatic RDNSS
// server.
func betterRDNSS(best, current system.IP) system.IP {
	if !best.Address.IsValid() {
		// When best is zero, current always wins.
		return current
	}

	// The best IPv6 address selection algorithm for RDNSS is as follows:
	//
	// 1) IP stability flags
	//   - flags which indicate an address is meant for stable, long-term use
	//     are more likely to serve traffic.
	// 2) Unique Local Address (ULA)
	//   - if assigned, high probability of use for internal-only services.
	// 3) Global Unicast Address (GUA)
	//   - de-facto choice when ULA is not available.
	// 4) Link-Local Address (LLA)
	//   - last resort, doesn't work across subnets but since this machine is
	//     also running CoreRAD (and sending router advertisements on-link) that
	//     may not be a problem.
	//
	// In the event of a tie, the lesser address by byte comparison wins.
	okC, okB := isStable(current), isStable(best)
	switch {
	case okC && !okB:
		// current wins.
		return current
	case !okC && okB:
		// best wins.
		return best
	}

	// Tie on flags, so now we have to compare IP address properties.
	var (
		cIP = current.Address.Addr()
		bIP = best.Address.Addr()
	)

	for _, fn := range []func(netip.Addr) bool{
		(netip.Addr).IsPrivate,
		(netip.Addr).IsGlobalUnicast,
		(netip.Addr).IsLinkLocalUnicast,
	} {
		okC, okB := fn(cIP), fn(bIP)
		switch {
		case okC && !okB:
			// current wins.
			return current
		case !okC && okB:
			// best wins.
			return best
		case okC && okB:
			// Tie, break using byte comparison.
			if cIP.Less(bIP) {
				return current
			}

			return best
		}
	}

	// None of the comparison functions were matched (perhaps due to an input
	// like localhost ::1), so do a final byte comparison.
	if cIP.Less(bIP) {
		return current
	}

	return best
}

// isStable indicates if ip is considered stable by its flag values.
func isStable(ip system.IP) bool {
	// No-op; used to align future checks and comments.
	return false ||
		// Indicates an address was statically assigned and we never expect it
		// to disappear while serving traffic.
		ip.ValidForever ||
		// Indicates the Linux kernel should allocate temporary addresses based
		// on this one: a good indicator of stability.
		ip.ManageTemporaryAddresses ||
		// Indicates an address which is stable per-network, and presumably a
		// machine serving router advertisements will remain on that network.
		ip.StablePrivacy ||
		// Last effort: does this address look like an EUI-64 format address?
		isEUI64(ip.Address.Addr())
}

// isEUI64 checks if ip resembles an IPv6 EUI-64 format address.
func isEUI64(ip netip.Addr) bool {
	// Look for the "ff:fe" pattern in the address.
	b := ip.As16()
	return b[11] == 0xff && b[12] == 0xfe
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
