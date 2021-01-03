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

package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/mdlayher/corerad/internal/plugin"
	"inet.af/netaddr"
)

// parsePlugin parses raw plugin configuration into a slice of plugins.
func parsePlugins(ifi rawInterface, maxInterval time.Duration, epoch time.Time) ([]plugin.Plugin, error) {
	var prefixes []*plugin.Prefix
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

	var routes []*plugin.Route
	for _, r := range ifi.Routes {
		rt, err := parseRoute(r)
		if err != nil {
			return nil, fmt.Errorf("failed to parse route %q: %v", r.Prefix, err)
		}

		routes = append(routes, rt)
	}

	// For sanity, configured routes on a given interface must not overlap.
	for _, rt1 := range routes {
		for _, rt2 := range routes {
			// Skip when rt1 and rt2 are identical, but make sure they don't
			// overlap otherwise.
			if rt1 != rt2 && rt1.Prefix.Overlaps(rt2.Prefix) {
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
		m := plugin.MTU(ifi.MTU)
		plugins = append(plugins, &m)
	}

	// Always set unless explicitly false.
	if ifi.SourceLLA == nil || *ifi.SourceLLA {
		plugins = append(plugins, &plugin.LLA{})
	}

	return plugins, nil
}

// parseDNSSL parses a DNSSL plugin.
func parseDNSSL(d rawDNSSL, maxInterval time.Duration) (*plugin.DNSSL, error) {
	lifetime, err := parseDuration(d.Lifetime)
	if err != nil {
		return nil, fmt.Errorf("invalid lifetime: %v", err)
	}

	// If auto, compute lifetime as recommended by radvd.
	if lifetime == durationAuto {
		lifetime = 2 * maxInterval
	}

	if len(d.DomainNames) == 0 {
		return nil, errors.New("must specify one or more DNS search domain names")
	}

	return &plugin.DNSSL{
		Lifetime:    lifetime,
		DomainNames: d.DomainNames,
	}, nil
}

// parsePrefix parses a Prefix plugin.
func parsePrefix(p rawPrefix, epoch time.Time) (*plugin.Prefix, error) {
	prefix, err := parseIPPrefix(p.Prefix)
	if err != nil {
		return nil, err
	}

	// Don't permit /128 "prefixes". This input is valid as a route but not
	// as a prefix.
	if prefix.IsSingleIP() {
		return nil, errors.New("/128 is a single IP address, not a prefix")
	}

	// Only permit ::/64 as a special case. It isn't clear if other prefix
	// lengths with :: would be useful, so throw an error for now.
	if prefix.IP == netaddr.IPv6Unspecified() && prefix.Bits != 64 {
		return nil, errors.New("only ::/64 is permitted for inferring prefixes from interface addresses")
	}

	valid, err := parseDuration(p.ValidLifetime)
	if err != nil {
		return nil, fmt.Errorf("invalid valid lifetime: %v", err)
	}

	// Use defaults for auto values.
	switch valid {
	case 0:
		return nil, errors.New("valid lifetime must be non-zero")
	case durationAuto:
		valid = 24 * time.Hour
	}

	preferred, err := parseDuration(p.PreferredLifetime)
	if err != nil {
		return nil, fmt.Errorf("invalid preferred lifetime: %v", err)
	}

	// Use defaults for auto values.
	switch preferred {
	case 0:
		return nil, errors.New("preferred lifetime must be non-zero")
	case durationAuto:
		preferred = 4 * time.Hour
	}

	// See: https://tools.ietf.org/html/rfc4861#section-4.6.2.
	if preferred > valid {
		return nil, fmt.Errorf("preferred lifetime of %s exceeds valid lifetime of %s",
			preferred, valid)
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
		Prefix:            prefix,
		OnLink:            onLink,
		Autonomous:        auto,
		ValidLifetime:     valid,
		PreferredLifetime: preferred,
		Deprecated:        p.Deprecated,
		Epoch:             epoch,
	}, nil
}

// parsePrefix parses a Prefix plugin.
func parseRoute(r rawRoute) (*plugin.Route, error) {
	prefix, err := parseIPPrefix(r.Prefix)
	if err != nil {
		return nil, err
	}

	pref, err := parsePreference(r.Preference)
	if err != nil {
		return nil, err
	}

	lt, err := parseDuration(r.Lifetime)
	if err != nil {
		return nil, fmt.Errorf("invalid lifetime: %v", err)
	}

	// Use defaults for auto values.
	switch lt {
	case 0:
		return nil, errors.New("lifetime must be non-zero")
	case durationAuto:
		lt = 24 * time.Hour
	}

	return &plugin.Route{
		Prefix:     prefix,
		Preference: pref,
		Lifetime:   lt,
	}, nil
}

// parseDNSSL parses a DNSSL plugin.
func parseRDNSS(d rawRDNSS, maxInterval time.Duration) (*plugin.RDNSS, error) {
	lifetime, err := parseDuration(d.Lifetime)
	if err != nil {
		return nil, fmt.Errorf("invalid lifetime: %v", err)
	}

	// If auto, compute lifetime as recommended by radvd.
	if lifetime == durationAuto {
		lifetime = 2 * maxInterval
	}

	if len(d.Servers) == 0 {
		return nil, errors.New("must specify one or more DNS server IPv6 addresses")
	}

	// Parse all server addresses as IPv6 addresses.
	servers := make([]netaddr.IP, 0, len(d.Servers))
	for _, s := range d.Servers {
		ip, err := netaddr.ParseIP(s)
		if err != nil {
			return nil, fmt.Errorf("failed to parse IP address %q: %v", s, err)
		}
		if !ip.Is6() {
			return nil, fmt.Errorf("string %q is not an IPv6 address", s)
		}

		servers = append(servers, ip)
	}

	return &plugin.RDNSS{
		Lifetime: lifetime,
		Servers:  servers,
	}, nil
}

// parseIPPrefix parses s an IPv6 prefix. It returns an error if the prefix is
// invalid, refers to an address within a prefix, or is an IPv4 prefix.
func parseIPPrefix(s string) (netaddr.IPPrefix, error) {
	p1, err := netaddr.ParseIPPrefix(s)
	if err != nil {
		return netaddr.IPPrefix{}, err
	}

	// Make sure that once masked (e.g. 2001:db8::1/64 becomes 2001:db8::/64)
	// the prefix is still identical. We do permit /128s because it's possible
	// to advertise routes that way, so prefixes and routes have their own
	// validation logic for that case.
	p2 := p1.Masked()
	if p1 != p2 {
		return netaddr.IPPrefix{}, errors.New("individual IP address, not a CIDR prefix")
	}

	// Only allow IPv6 addresses.
	if !p1.IP.Is6() || p1.IP.Is4in6() {
		return netaddr.IPPrefix{}, errors.New("not an IPv6 CIDR prefix")
	}

	return p1, nil
}
