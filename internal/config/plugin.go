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
	"net"
	"time"

	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mikioh/ipaddr"
)

// parsePlugin parses raw plugin configuration into a slice of plugins.
func parsePlugins(ifi rawInterface, maxInterval time.Duration) ([]plugin.Plugin, error) {
	var prefixes []*plugin.Prefix
	for _, p := range ifi.Prefixes {
		pfx, err := parsePrefix(p)
		if err != nil {
			return nil, fmt.Errorf("failed to parse prefix %q: %v", p.Prefix, err)
		}

		prefixes = append(prefixes, pfx)
	}

	// For sanity, configured prefixes on a given interface must not overlap.
	for _, pfx1 := range prefixes {
		for _, pfx2 := range prefixes {
			p1, p2 := ipaddr.NewPrefix(pfx1.Prefix), ipaddr.NewPrefix(pfx2.Prefix)
			if !p1.Equal(p2) && p1.Overlaps(p2) {
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
			p1, p2 := ipaddr.NewPrefix(rt1.Prefix), ipaddr.NewPrefix(rt2.Prefix)
			if !p1.Equal(p2) && p1.Overlaps(p2) {
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
func parsePrefix(p rawPrefix) (*plugin.Prefix, error) {
	ip, prefix, err := net.ParseCIDR(p.Prefix)
	if err != nil {
		return nil, err
	}

	// Don't allow individual IP addresses.
	if !prefix.IP.Equal(ip) {
		return nil, fmt.Errorf("%q is not a CIDR prefix", ip)
	}

	// Only allow IPv6 addresses.
	if prefix.IP.To16() != nil && prefix.IP.To4() != nil {
		return nil, fmt.Errorf("%q is not an IPv6 CIDR prefix", prefix.IP)
	}

	// Only permit ::/64 as a special case. It isn't clear if other prefix
	// lengths with :: would be useful, so throw an error for now.
	length, _ := prefix.Mask.Size()
	if prefix.IP.Equal(net.IPv6zero) && length != 64 {
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
	}, nil
}

// parsePrefix parses a Prefix plugin.
func parseRoute(r rawRoute) (*plugin.Route, error) {
	ip, prefix, err := net.ParseCIDR(r.Prefix)
	if err != nil {
		return nil, err
	}

	// Don't allow individual IP addresses.
	if !prefix.IP.Equal(ip) {
		return nil, fmt.Errorf("%q is not a CIDR prefix", ip)
	}

	// Only allow IPv6 addresses.
	if prefix.IP.To16() != nil && prefix.IP.To4() != nil {
		return nil, fmt.Errorf("%q is not an IPv6 CIDR prefix", prefix.IP)
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
	servers := make([]net.IP, 0, len(d.Servers))
	for _, s := range d.Servers {
		ip := net.ParseIP(s)
		if ip == nil || (ip.To16() != nil && ip.To4() != nil) {
			return nil, fmt.Errorf("string %q is not an IPv6 address", s)
		}

		servers = append(servers, ip)
	}

	return &plugin.RDNSS{
		Lifetime: lifetime,
		Servers:  servers,
	}, nil
}
