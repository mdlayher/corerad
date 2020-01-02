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
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mdlayher/ndp"
)

// A Plugin specifies a CoreRAD plugin's configuration.
type Plugin interface {
	// Name is the string name of the plugin.
	Name() string

	// String is the string representation of the plugin's configuration.
	String() string

	// Decode decodes raw TOML configuration into a Plugin's specific
	// configuration.
	Decode(md toml.MetaData, m map[string]toml.Primitive) error
}

// parsePlugin parses raw plugin key/values into a Plugin.
func parsePlugin(iface Interface, md toml.MetaData, m map[string]toml.Primitive) (Plugin, error) {
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
	var p Plugin
	switch name {
	case "dnssl":
		p = &DNSSL{Lifetime: DurationAuto}
	case "mtu":
		p = new(MTU)
	case "prefix":
		p = NewPrefix()
	case "rdnss":
		p = &RDNSS{Lifetime: DurationAuto}
	default:
		return nil, fmt.Errorf("unknown plugin %q", name)
	}

	if err := p.Decode(md, m); err != nil {
		return nil, fmt.Errorf("failed to configure plugin %q: %v", p.Name(), err)
	}

	// Finish computing any auto values.
	computeAuto(iface, p)

	return p, nil
}

// computeAuto applies configuration from iface to p for values set to auto.
func computeAuto(iface Interface, p Plugin) {
	switch p := p.(type) {
	case *DNSSL:
		// If auto, compute lifetime as recommended by radvd.
		if p.Lifetime == DurationAuto {
			p.Lifetime = 2 * iface.MaxInterval
		}
	case *RDNSS:
		// See DNSSL.
		if p.Lifetime == DurationAuto {
			p.Lifetime = 2 * iface.MaxInterval
		}
	}
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

// Decode implements Plugin.
func (d *DNSSL) Decode(md toml.MetaData, m map[string]toml.Primitive) error {
	for k := range m {
		var v value
		if err := md.PrimitiveDecode(m[k], &v.v); err != nil {
			return err
		}

		switch k {
		case "lifetime":
			d.Lifetime = v.Duration()
		case "domain_names":
			d.DomainNames = v.StringSlice()
		default:
			return fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

	return nil
}

// A Prefix configures a NDP Prefix Information option.
type Prefix struct {
	Prefix            *net.IPNet
	OnLink            bool
	Autonomous        bool
	ValidLifetime     time.Duration
	PreferredLifetime time.Duration
}

// NewPrefix creates a Prefix with default values configured as specified
// by radvd.
func NewPrefix() *Prefix {
	return &Prefix{
		OnLink:            true,
		Autonomous:        true,
		ValidLifetime:     24 * time.Hour,
		PreferredLifetime: 4 * time.Hour,
	}
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

// Decode implements Plugin.
func (p *Prefix) Decode(md toml.MetaData, m map[string]toml.Primitive) error {
	for k := range m {
		var v value
		if err := md.PrimitiveDecode(m[k], &v.v); err != nil {
			return err
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
			return fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

	return p.validate()
}

// validate verifies that a Prefix is valid.
func (p *Prefix) validate() error {
	if p.Prefix == nil {
		return errors.New("prefix must not be empty")
	}

	// Use defaults for auto values.
	def := NewPrefix()
	switch p.ValidLifetime {
	case 0:
		return errors.New("valid lifetime must be non-zero")
	case DurationAuto:
		p.ValidLifetime = def.ValidLifetime
	}

	switch p.PreferredLifetime {
	case 0:
		return errors.New("preferred lifetime must be non-zero")
	case DurationAuto:
		p.PreferredLifetime = def.PreferredLifetime
	}

	// See: https://tools.ietf.org/html/rfc4861#section-4.6.2.
	if p.PreferredLifetime > p.ValidLifetime {
		return fmt.Errorf("preferred lifetime of %s exceeds valid lifetime of %s", p.PreferredLifetime, p.ValidLifetime)
	}

	return nil
}

// MTU configures a NDP MTU option.
type MTU int

// Name implements Plugin.
func (m *MTU) Name() string { return "mtu" }

// String implements Plugin.
func (m *MTU) String() string { return fmt.Sprintf("MTU: %d", *m) }

// Decode implements Plugin.
func (m *MTU) Decode(md toml.MetaData, mp map[string]toml.Primitive) error {
	for k := range mp {
		var v value
		if err := md.PrimitiveDecode(mp[k], &v.v); err != nil {
			return err
		}

		switch k {
		case "mtu":
			// Loopback has an MTU of 65536 on Linux. Good enough?
			*m = MTU(v.Int(0, 65536))
		default:
			return fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

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

// Decode implements Plugin.
func (r *RDNSS) Decode(md toml.MetaData, m map[string]toml.Primitive) error {
	for k := range m {
		var v value
		if err := md.PrimitiveDecode(m[k], &v.v); err != nil {
			return err
		}

		switch k {
		case "lifetime":
			r.Lifetime = v.Duration()
		case "servers":
			r.Servers = v.IPSlice()
		default:
			return fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

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
