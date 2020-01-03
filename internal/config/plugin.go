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

	"github.com/BurntSushi/toml"
	"github.com/mdlayher/corerad/internal/plugin"
)

// parsePlugin parses raw plugin key/values into a Plugin.
func parsePlugin(iface Interface, md toml.MetaData, m map[string]toml.Primitive) (plugin.Plugin, error) {
	// Each plugin is identified by a name. Once we parse the name, clear it
	// from the map so the plugins themselves don't have to handle it.
	pname, ok := m["name"]
	if !ok {
		return nil, errors.New(`missing "name" key for plugin`)
	}
	delete(m, "name")

	var name string
	if err := md.PrimitiveDecode(pname, &name); err != nil {
		return nil, err
	}

	// Now that we know the plugin's name, we can initialize the specific Plugin
	// required and decode its individual configuration.
	var (
		p   plugin.Plugin
		err error
	)
	switch name {
	case "dnssl":
		p, err = parseDNSSL(iface, md, m)
	case "mtu":
		p, err = parseMTU(md, m)
	case "prefix":
		p, err = parsePrefix(md, m)
	case "rdnss":
		p, err = parseRDNSS(iface, md, m)
	default:
		return nil, fmt.Errorf("unknown plugin %q", name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to configure plugin %q: %v", name, err)
	}

	return p, nil
}

// parseDNSSL parses a DNSSL plugin.
func parseDNSSL(iface Interface, md toml.MetaData, m map[string]toml.Primitive) (*plugin.DNSSL, error) {
	d := &plugin.DNSSL{Lifetime: DurationAuto}
	for k := range m {
		var v value
		if err := md.PrimitiveDecode(m[k], &v.v); err != nil {
			return nil, err
		}

		switch k {
		case "lifetime":
			d.Lifetime = v.Duration()
		case "domain_names":
			d.DomainNames = v.StringSlice()
		default:
			return nil, fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return nil, fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

	// If auto, compute lifetime as recommended by radvd.
	if d.Lifetime == DurationAuto {
		d.Lifetime = 2 * iface.MaxInterval
	}

	return d, nil
}

// parsePrefix parses a Prefix plugin.
func parsePrefix(md toml.MetaData, m map[string]toml.Primitive) (*plugin.Prefix, error) {
	p := plugin.NewPrefix()

	for k := range m {
		var v value
		if err := md.PrimitiveDecode(m[k], &v.v); err != nil {
			return nil, err
		}

		switch k {
		case "autonomous":
			p.Autonomous = v.Bool()
		case "on_link":
			p.OnLink = v.Bool()
		case "preferred_lifetime":
			p.PreferredLifetime = v.Duration()
		case "prefix":
			p.Prefix = v.IPNet()
		case "valid_lifetime":
			p.ValidLifetime = v.Duration()
		default:
			return nil, fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return nil, fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

	if err := validatePrefix(p); err != nil {
		return nil, err
	}

	return p, nil
}

// validatePrefix verifies that a Prefix is valid.
func validatePrefix(p *plugin.Prefix) error {
	if p.Prefix == nil {
		return errors.New("prefix must not be empty")
	}

	// Use defaults for auto values.
	switch p.ValidLifetime {
	case 0:
		return errors.New("valid lifetime must be non-zero")
	case DurationAuto:
		p.ValidLifetime = 24 * time.Hour
	}

	switch p.PreferredLifetime {
	case 0:
		return errors.New("preferred lifetime must be non-zero")
	case DurationAuto:
		p.PreferredLifetime = 4 * time.Hour
	}

	// See: https://tools.ietf.org/html/rfc4861#section-4.6.2.
	if p.PreferredLifetime > p.ValidLifetime {
		return fmt.Errorf("preferred lifetime of %s exceeds valid lifetime of %s", p.PreferredLifetime, p.ValidLifetime)
	}

	return nil
}

// parseMTU parses a MTU plugin.
func parseMTU(md toml.MetaData, mp map[string]toml.Primitive) (*plugin.MTU, error) {
	var m plugin.MTU
	for k := range mp {
		var v value
		if err := md.PrimitiveDecode(mp[k], &v.v); err != nil {
			return nil, err
		}

		switch k {
		case "mtu":
			// Loopback has an MTU of 65536 on Linux. Good enough?
			m = plugin.MTU(v.Int(0, 65536))
		default:
			return nil, fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return nil, fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

	return &m, nil
}

// parseRDNSS parses a RDNSS plugin.
func parseRDNSS(iface Interface, md toml.MetaData, m map[string]toml.Primitive) (*plugin.RDNSS, error) {
	r := &plugin.RDNSS{Lifetime: DurationAuto}
	for k := range m {
		var v value
		if err := md.PrimitiveDecode(m[k], &v.v); err != nil {
			return nil, err
		}

		switch k {
		case "lifetime":
			r.Lifetime = v.Duration()
		case "servers":
			r.Servers = v.IPSlice()
		default:
			return nil, fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return nil, fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

	// If auto, compute lifetime as recommended by radvd.
	if r.Lifetime == DurationAuto {
		r.Lifetime = 2 * iface.MaxInterval
	}

	return r, nil
}
