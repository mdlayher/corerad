// Copyright 2019-2022 Matt Layher
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

package config

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/ndp"
)

// The Well-Known Prefix for IPv4 to IPv6 translation, as specified in RFC
// 6052 Section 2.1.
const defaultPREF64Prefix = "64:ff9b::/96"

// parsePlugin parses raw plugin configuration into a slice of plugins.
func parsePlugins(ifi rawInterface, maxInterval time.Duration, epoch time.Time) ([]plugin.Plugin, error) {
	prefixes := make([]*plugin.Prefix, 0, len(ifi.Prefixes))
	for _, p := range ifi.Prefixes {
		pfx, err := parsePrefix(p, epoch)
		if err != nil {
			return nil, fmt.Errorf("failed to parse prefix %q: %v", p.Prefix, err)
		}

		prefixes = append(prefixes, pfx)
	}

	// For sanity, configured prefixes on a given interface must not overlap.
	for _, pfx1 := range prefixes {
		for _, pfx2 := range prefixes {
			// Skip when pfx1 and pfx2 are identical, but make sure they don't
			// overlap otherwise.
			if pfx1 != pfx2 && pfx1.Prefix.Overlaps(pfx2.Prefix) {
				return nil, fmt.Errorf("prefixes overlap: %s and %s",
					pfx1.Prefix, pfx2.Prefix)
			}
		}
	}

	// Now that we've verified the prefixes, add them as plugins.
	plugins := make([]plugin.Plugin, 0, len(prefixes))
	for _, p := range prefixes {
		plugins = append(plugins, p)
	}

	routes := make([]*plugin.Route, 0, len(ifi.Routes))
	for _, r := range ifi.Routes {
		rt, err := parseRoute(r, epoch)
		if err != nil {
			return nil, fmt.Errorf("failed to parse route %q: %v", r.Prefix, err)
		}

		routes = append(routes, rt)
	}

	// Configured routes on a given interface must not overlap. This would
	// produce confusing and ineffective output to clients.
	for _, rt1 := range routes {
		for _, rt2 := range routes {
			// Skip when rt1 and rt2 are identical or one of them is the auto
			// route.
			if rt1 == rt2 || rt1.Prefix == autoRoute || rt2.Prefix == autoRoute {
				continue
			}

			if rt1.Prefix.Overlaps(rt2.Prefix) {
				return nil, fmt.Errorf("routes overlap: %s and %s",
					rt1.Prefix, rt2.Prefix)
			}
		}
	}

	for _, r := range routes {
		plugins = append(plugins, r)
	}

	for _, r := range ifi.RDNSS {
		rdnss, err := parseRDNSS(r, maxInterval)
		if err != nil {
			return nil, fmt.Errorf("failed to parse RDNSS: %v", err)
		}

		plugins = append(plugins, rdnss)
	}

	for _, d := range ifi.DNSSL {
		dnssl, err := parseDNSSL(d, maxInterval)
		if err != nil {
			return nil, fmt.Errorf("failed to parse DNSSL: %v", err)
		}

		plugins = append(plugins, dnssl)
	}

	// Loopback has an MTU of 65536 on Linux. Good enough?
	if ifi.MTU < 0 || ifi.MTU > 65536 {
		return nil, fmt.Errorf("MTU (%d) must be between 0 and 65536", ifi.MTU)
	}
	if ifi.MTU != 0 {
		plugins = append(plugins, plugin.NewMTU(ifi.MTU))
	}

	// Always set unless explicitly false.
	if ifi.SourceLLA == nil || *ifi.SourceLLA {
		plugins = append(plugins, &plugin.LLA{})
	}

	// Only set when key is not empty.
	//
	// TODO(mdlayher): advertise ndp.Unrestricted by default if empty?
	if cp := ifi.CaptivePortal; cp != "" {
		cp, err := plugin.NewCaptivePortal(cp)
		if err != nil {
			return nil, err
		}

		plugins = append(plugins, cp)
	}

	for _, p := range ifi.PREF64 {
		base := defaultPREF64Prefix
		if p.Prefix != nil && *p.Prefix != "" {
			// Use the caller's prefix.
			base = *p.Prefix
		}

		prefix, err := netip.ParsePrefix(base)
		if err != nil {
			return nil, err
		}

		plugins = append(plugins, plugin.NewPREF64(prefix, maxInterval))
	}

	return plugins, nil
}

// parseDNSSL parses a DNSSL plugin.
func parseDNSSL(d rawDNSSL, maxInterval time.Duration) (*plugin.DNSSL, error) {
	// By default, compute lifetime as recommended by radvd.
        // As per RFC8106, the default lifetime SHOULD be at least
        // 3 * MaxRtrAdvInterval.
	lifetime, err := parseDuration(d.Lifetime, 3*maxInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid lifetime: %v", err)
	}

	if len(d.DomainNames) == 0 {
		return nil, errors.New("must specify one or more DNS search domain names")
	}

	// Make sure all domain names are unique.
	seen := make(map[string]struct{})
	for _, d := range d.DomainNames {
		if _, ok := seen[d]; ok {
			return nil, fmt.Errorf("domain name %q cannot be specified multiple times", d)
		}
		seen[d] = struct{}{}
	}

	return &plugin.DNSSL{
		Lifetime:    lifetime,
		DomainNames: d.DomainNames,
	}, nil
}

// autoPrefix is the sentinel prefix which is used to automatically infer the
// appropriate prefixes to advertise for a given interface.
var autoPrefix = netip.MustParsePrefix("::/64")

// parsePrefix parses a Prefix plugin.
func parsePrefix(p rawPrefix, epoch time.Time) (*plugin.Prefix, error) {
	prefix, err := parseIPPrefix(p.Prefix)
	if err != nil {
		return nil, err
	}

	// Allow an empty prefix to be used in place of ::/64 as that is the default
	// most users will want.
	if !prefix.IsValid() {
		prefix = autoPrefix
	}

	// Don't permit /128 "prefixes". This input is valid as a route but not
	// as a prefix.
	if prefix.IsSingleIP() {
		return nil, errors.New("/128 is a single IP address, not a prefix")
	}

	// Only permit ::/64 as a special case. It isn't clear if other prefix
	// lengths with :: would be useful, so throw an error for now.
	if prefix.Addr().IsUnspecified() && prefix.Bits() != 64 {
		return nil, errors.New("only ::/64 is permitted for inferring prefixes from interface addresses")
	}

	valid, err := parseDuration(p.ValidLifetime, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("invalid valid lifetime: %v", err)
	}

	if valid == 0 {
		return nil, errors.New("valid lifetime must be non-zero")
	}

	preferred, err := parseDuration(p.PreferredLifetime, 4*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("invalid preferred lifetime: %v", err)
	}

	// Use defaults for auto values.
	if preferred == 0 {
		return nil, errors.New("preferred lifetime must be non-zero")
	}

	// See: https://tools.ietf.org/html/rfc4861#section-4.6.2.
	if preferred > valid {
		return nil, fmt.Errorf("preferred lifetime of %s exceeds valid lifetime of %s",
			preferred, valid)
	}

	// Deprecated prefixes cannot have infinite lifetimes.
	if p.Deprecated && (preferred == ndp.Infinity || valid == ndp.Infinity) {
		return nil, errors.New("prefix is deprecated and cannot have infinite preferred or valid lifetimes")
	}

	onLink := true
	if p.OnLink != nil {
		onLink = *p.OnLink
	}

	auto := true
	if p.Autonomous != nil {
		auto = *p.Autonomous
	}

	return &plugin.Prefix{
		// Determine whether or not the prefix is using auto mode.
		Auto: prefix == autoPrefix,

		Prefix:            prefix,
		OnLink:            onLink,
		Autonomous:        auto,
		ValidLifetime:     valid,
		PreferredLifetime: preferred,
		Deprecated:        p.Deprecated,
		Epoch:             epoch,
	}, nil
}

// autoRoute is the sentinel prefix which is used to automatically infer the
// appropriate routes to advertise for a given interface.
var autoRoute = netip.MustParsePrefix("::/0")

// parseRoute parses a Route plugin.
func parseRoute(r rawRoute, epoch time.Time) (*plugin.Route, error) {
	prefix, err := parseIPPrefix(r.Prefix)
	if err != nil {
		return nil, err
	}

	// Allow an empty prefix to be used in place of ::/0 as that is the default
	// most users will want.
	if !prefix.IsValid() {
		prefix = autoRoute
	}

	// Only permit ::/0 as a special case for now. It might make sense to allow
	// for example ::/48 or ::/56 in the future to match any route of that
	// length or longer, but this would be a natural extension of ::/0 ("any
	// length").
	if prefix.Addr().IsUnspecified() && prefix.Bits() != 0 {
		return nil, errors.New("only ::/0 is permitted for inferring routes from loopback interfaces")
	}

	pref, err := parsePreference(r.Preference)
	if err != nil {
		return nil, err
	}

	lt, err := parseDuration(r.Lifetime, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("invalid lifetime: %v", err)
	}

	if lt == 0 {
		return nil, errors.New("lifetime must be non-zero")
	}

	// Deprecated routes cannot have an infinite lifetime.
	if r.Deprecated && lt == ndp.Infinity {
		return nil, errors.New("route is deprecated and cannot have an infinite lifetime")
	}

	return &plugin.Route{
		// Determine whether or not the route is using auto mode.
		Auto: prefix == autoRoute,

		Prefix:     prefix,
		Preference: pref,
		Lifetime:   lt,
		Deprecated: r.Deprecated,
		Epoch:      epoch,
	}, nil
}

// parseRDNSS parses a RDNSS plugin.
func parseRDNSS(d rawRDNSS, maxInterval time.Duration) (*plugin.RDNSS, error) {
	// If auto, compute lifetime as recommended by radvd.
        // As per RFC8106, the default lifetime SHOULD be at least
        // 3 * MaxRtrAdvInterval.
	lifetime, err := parseDuration(d.Lifetime, 3*maxInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid lifetime: %v", err)
	}

	if len(d.Servers) == 0 {
		// If the RDNSS stanza was specified but the servers list is empty,
		// assume the user wants the :: wildcard.
		return &plugin.RDNSS{
			Auto: true,

			Lifetime: lifetime,
		}, nil
	}

	// Parse all server addresses as unique IPv6 addresses.
	var (
		auto    bool
		servers = make(map[netip.Addr]struct{})
	)

	for _, s := range d.Servers {
		ip, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("failed to parse IP address %q: %v", s, err)
		}
		if !ip.Is6() || ip.Is4In6() {
			return nil, fmt.Errorf("string %q is not an IPv6 address", s)
		}

		// If :: is present, don't add it to the slice but do set Auto to true
		// so a server address can be automatically chosen at runtime. The
		// remaining server addresses will be set statically.
		if ip.IsUnspecified() {
			// Don't allow :: twice. This check is separate because we don't
			// actually add it to the servers set.
			if auto {
				return nil, errors.New("server wildcard :: cannot be specified multiple times")
			}

			auto = true
			continue
		}

		// Don't allow repeated servers.
		if _, ok := servers[ip]; ok {
			return nil, fmt.Errorf("server %q cannot be specified multiple times", ip)
		}
		servers[ip] = struct{}{}
	}

	// If any servers are present, flatten the set into a slice and sort for
	// deterministic output.
	var ips []netip.Addr
	if len(servers) > 0 {
		ips = make([]netip.Addr, 0, len(servers))
		for ip := range servers {
			ips = append(ips, ip)
		}

		slices.SortStableFunc(ips, func(a, b netip.Addr) int { return a.Compare(b) })
	}

	return &plugin.RDNSS{
		Auto: auto,

		Lifetime: lifetime,
		Servers:  ips,
	}, nil
}

// parseIPPrefix parses s an IPv6 prefix which may optionally be empty. It
// returns an error if the prefix is invalid, refers to an address within a
// prefix, or is an IPv4 prefix.
func parseIPPrefix(s string) (netip.Prefix, error) {
	if s == "" {
		// Empty string produces a valid zero IPPrefix.
		return netip.Prefix{}, nil
	}

	p1, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, err
	}

	// Make sure that once masked (e.g. 2001:db8::1/64 becomes 2001:db8::/64)
	// the prefix is still identical. We do permit /128s because it's possible
	// to advertise routes that way, so prefixes and routes have their own
	// validation logic for that case.
	p2 := p1.Masked()
	if p1 != p2 {
		return netip.Prefix{}, errors.New("individual IP address, not a CIDR prefix")
	}

	// Only allow IPv6 addresses.
	if !p1.Addr().Is6() || p1.Addr().Is4In6() {
		return netip.Prefix{}, errors.New("not an IPv6 CIDR prefix")
	}

	return p1, nil
}
