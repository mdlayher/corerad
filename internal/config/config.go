// Copyright 2019-2021 Matt Layher
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
	"io"
	"net"
	"time"

	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/ndp"
	"github.com/pelletier/go-toml"
)

// Minimal is the minimal configuration file for CoreRAD.
const Minimal = `# %s configuration file

# Advertise an IPv6 default route and SLAAC-capable prefixes on LAN-facing eth0.
[[interfaces]]
name = "eth0"
advertise = true

  # Advertise an on-link, autonomous prefix for all /64 addresses on eth0.
  [[interfaces.prefix]]

# Monitor upstream router advertisements on WAN-facing eth1.
[[interfaces]]
name = "eth1"
monitor = true

# Optional: enable Prometheus metrics.
[debug]
address = "localhost:9430"
prometheus = true
`

// A file is the raw top-level configuration file representation.
type file struct {
	Interfaces []rawInterface `toml:"interfaces"`
	Debug      Debug          `toml:"debug"`
}

// A rawInterface is the raw configuration file representation of an Interface.
type rawInterface struct {
	// Identifier for a single interface or multiple interfaces which should be
	// configured as a unit: mutually exclusive.
	Name  string   `toml:"name"`
	Names []string `toml:"names"`

	// Base interface configuration.
	Monitor         bool    `toml:"monitor"`
	Advertise       bool    `toml:"advertise"`
	Verbose         bool    `toml:"verbose"`
	MaxInterval     string  `toml:"max_interval"`
	MinInterval     string  `toml:"min_interval"`
	Managed         bool    `toml:"managed"`
	OtherConfig     bool    `toml:"other_config"`
	ReachableTime   string  `toml:"reachable_time"`
	RetransmitTimer string  `toml:"retransmit_timer"`
	HopLimit        *int    `toml:"hop_limit"`
	DefaultLifetime *string `toml:"default_lifetime"`
	UnicastOnly     bool    `toml:"unicast_only"`
	Preference      string  `toml:"preference"`

	// Plugins.
	//
	// TOML tags for slices are explicitly singular.
	Prefixes      []rawPrefix `toml:"prefix"`
	Routes        []rawRoute  `toml:"route"`
	RDNSS         []rawRDNSS  `toml:"rdnss"`
	DNSSL         []rawDNSSL  `toml:"dnssl"`
	MTU           int         `toml:"mtu"`
	SourceLLA     *bool       `toml:"source_lla"`
	CaptivePortal string      `toml:"captive_portal"`
}

// A rawPrefix is the raw configuration file representation of a Prefix plugin.
type rawPrefix struct {
	Prefix            string  `toml:"prefix"`
	OnLink            *bool   `toml:"on_link"`
	Autonomous        *bool   `toml:"autonomous"`
	ValidLifetime     *string `toml:"valid_lifetime"`
	PreferredLifetime *string `toml:"preferred_lifetime"`
	Deprecated        bool    `toml:"deprecated"`
}

// A rawRoute is the raw configuration file representation of a Route plugin.
type rawRoute struct {
	Prefix     string  `toml:"prefix"`
	Preference string  `toml:"preference"`
	Lifetime   *string `toml:"lifetime"`
	Deprecated bool    `toml:"deprecated"`
}

// A rawDNSSL is the raw configuration file representation of a DNSSL plugin.
type rawDNSSL struct {
	Lifetime    *string  `toml:"lifetime"`
	DomainNames []string `toml:"domain_names"`
}

// A rawRDNSS is the raw configuration file representation of a RDNSS plugin.
type rawRDNSS struct {
	Lifetime *string  `toml:"lifetime"`
	Servers  []string `toml:"servers"`
}

// Config specifies the configuration for CoreRAD.
type Config struct {
	// User-specified.
	Interfaces []Interface
	Debug      Debug
}

// An Interface provides configuration for an individual interface.
type Interface struct {
	Name                           string
	Monitor, Advertise, Verbose    bool
	MinInterval, MaxInterval       time.Duration
	Managed, OtherConfig           bool
	ReachableTime, RetransmitTimer time.Duration
	HopLimit                       uint8
	DefaultLifetime                time.Duration
	UnicastOnly                    bool
	Preference                     ndp.Preference
	Plugins                        []plugin.Plugin
}

// RouterAdvertisement generates an IPv6 NDP router advertisement for this
// interface. Input parameters are used to tune parts of the RA, per the
// NDP RFCs.
func (ifi Interface) RouterAdvertisement(forwarding bool) (*ndp.RouterAdvertisement, error) {
	ra := &ndp.RouterAdvertisement{
		CurrentHopLimit:           ifi.HopLimit,
		ManagedConfiguration:      ifi.Managed,
		OtherConfiguration:        ifi.OtherConfig,
		RouterSelectionPreference: ifi.Preference,
		RouterLifetime:            ifi.DefaultLifetime,
		ReachableTime:             ifi.ReachableTime,
		RetransmitTimer:           ifi.RetransmitTimer,
	}

	for _, p := range ifi.Plugins {
		if err := p.Apply(ra); err != nil {
			return nil, fmt.Errorf("failed to apply plugin %q: %v", p.Name(), err)
		}
	}

	// Apply any necessary changes due to modification in system state.

	// If the interface is not forwarding packets, we must set the router
	// lifetime field to zero, per:
	// https://tools.ietf.org/html/rfc4861#section-6.2.5.
	if !forwarding {
		ra.RouterLifetime = 0
	}

	return ra, nil
}

// Debug provides configuration for debugging and observability.
type Debug struct {
	Address    string `toml:"address"`
	Prometheus bool   `toml:"prometheus"`
	PProf      bool   `toml:"pprof"`
}

// Parse parses a Config in TOML format from an io.Reader and verifies that
// the configuration is valid. If the epoch is not zero, it is used to calculate
// deprecation times for certain parameters.
func Parse(r io.Reader, epoch time.Time) (*Config, error) {
	var f file
	if err := toml.NewDecoder(r).Strict(true).Decode(&f); err != nil {
		return nil, err
	}

	// Must configure at least one interface.
	if len(f.Interfaces) == 0 {
		return nil, errors.New("no configured interfaces")
	}

	c := &Config{
		Interfaces: make([]Interface, 0, len(f.Interfaces)),
	}

	// Validate debug configuration if set.
	if f.Debug.Address != "" {
		if _, err := net.ResolveTCPAddr("tcp", f.Debug.Address); err != nil {
			return nil, fmt.Errorf("bad debug address: %v", err)
		}
		c.Debug = f.Debug
	}

	// Make sure each interface appears only once, but don't bother to check for
	// valid interface names: that is more easily done when trying to create
	// server listeners.
	seen := make(map[string]struct{})
	for i, ifaces := range f.Interfaces {
		ifis, err := parseInterfaces(ifaces, epoch)
		if err != nil {
			// Narrow down the location of a configuration error.
			return nil, fmt.Errorf("interface %d: %v", i, err)
		}

		for _, ifi := range ifis {
			if _, ok := seen[ifi.Name]; ok {
				return nil, fmt.Errorf("interface %d: %q cannot appear multiple times in configuration", i, ifi.Name)
			}
			seen[ifi.Name] = struct{}{}
		}

		c.Interfaces = append(c.Interfaces, ifis...)
	}

	return c, nil
}

// parseDuration parses a duration while also recognizing special values such
// as auto and infinite. If the key is unset or auto, def is used.
func parseDuration(s *string, def time.Duration) (time.Duration, error) {
	if s == nil {
		// Nil implies the key is not set at all, so use the default.
		return def, nil
	}

	switch *s {
	case "infinite":
		return ndp.Infinity, nil
	case "auto":
		return def, nil
	case "":
		// Empty string implies the key is set but has no value, therefore we
		// should use zero.
		return 0, nil
	}

	// Use the user's value, but validate it per the RFC.
	return time.ParseDuration(*s)
}
