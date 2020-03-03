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
	"io"
	"net"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/ndp"
)

//go:generate embed file -var Default --source default.toml

// Default is the toml representation of the default configuration.
var Default = "# %s configuration file\n\n# All duration values are specified in Go time.ParseDuration format:\n# https://golang.org/pkg/time/#ParseDuration.\n\n# Interfaces which will be used to serve IPv6 NDP router advertisements.\n[[interfaces]]\nname = \"eth0\"\n\n# AdvSendAdvertisements: indicates whether or not this interface will send\n# periodic router advertisements and respond to router solicitations.\n#\n# Must be set to true to enable serving on this interface.\nadvertise = false\n\n# All other interface parameters in this section can be removed to simplify\n# configuration with sane defaults.\n\n# MaxRtrAdvInterval: the maximum time between sending unsolicited multicast\n# router advertisements. Must be between 4 and 1800 seconds.\nmax_interval = \"600s\"\n\n# MinRtrAdvInterval: the minimum time between sending unsolicited multicast\n# router advertisements. Must be between 3 and (.75 * max_interval) seconds.\n# An empty string or the value \"auto\" will compute a sane default.\nmin_interval = \"auto\"\n\n# AdvManagedFlag: indicates if hosts should request address configuration from a\n# DHCPv6 server.\nmanaged = false\n\n# AdvOtherConfigFlag: indicates if additional configuration options are\n# available from a DHCPv6 server.\nother_config = false\n\n# AdvReachableTime: indicates how long a node should treat a neighbor as\n# reachable. 0 or empty string mean this value is unspecified by this router.\nreachable_time = \"0s\"\n\n# AdvRetransTimer: indicates how long a node should wait before retransmitting\n# neighbor solicitations. 0 or empty string mean this value is unspecified by\n# this router.\nretransmit_timer = \"0s\"\n\n# AdvCurHopLimit: indicates the value that should be placed in the Hop Limit\n# field in the IPv6 header. Must be between 0 and 255. 0 means this value\n# is unspecified by this router.\nhop_limit = 64\n\n# AdvDefaultLifetime: the value sent in the router lifetime field. Must be\n# 0 or between max_interval and 9000 seconds. An empty string is treated as 0,\n# or the value \"auto\" will compute a sane default.\ndefault_lifetime = \"auto\"\n\n# AdvLinkMTU: attaches a NDP MTU option to the router advertisement, so clients\n# can set their link MTU as recommended by the router. 0 means this value is\n# unspecified by this router.\nmtu = 0\n\n# AdvSourceLLAddress: attaches a NDP source link-layer address option to the\n# router advertisement. Defaults to true when omitted.\nsource_lla = true\n\n# Indicates whether or not CoreRAD will issue multicast router advertisements.\n# In this mode, machines on this interface's LAN must issue individual router\n# solicitations in order to receive router advertisements.\nunicast_only = false\n\n# Indicates the preference of this router over other default routers. Only the\n# values \"low\", \"medium\", and \"high\" are allowed. An empty string is treated as\n# \"medium\".\npreference = \"medium\"\n\n  # Prefix: attaches a NDP Prefix Information option to the router advertisement.\n  [[interfaces.prefix]]\n  # Serve Prefix Information options for each IPv6 prefix on this interface\n  # configured with a /64 CIDR mask. Only /64 is allowed for this special case.\n  prefix = \"::/64\"\n\n  # Specifies on-link and autonomous address autoconfiguration (SLAAC) flags\n  # for this prefix. Both default to true.\n  on_link = true\n  autonomous = true\n\n  # Specifies the preferred and valid lifetimes for this prefix. The preferred\n  # lifetime must not exceed the valid lifetime. By default, the preferred\n  # lifetime is 4 hours and the valid lifetime is 24 hours. \"auto\" uses the\n  # defaults. \"infinite\" means this prefix should be used forever.\n  preferred_lifetime = \"auto\"\n  valid_lifetime = \"auto\"\n\n  # Alternatively, serve an explicit IPv6 prefix.\n  [[interfaces.prefix]]\n  prefix = \"2001:db8::/64\"\n\n  # Route: attaches a NDP Route Information option to the router advertisement.\n  [[interfaces.route]]\n  prefix = \"2001:db8:ffff::/64\"\n\n  # Indicates the preference of this route over other routes advertised by\n  # other routers. Only the values \"low\", \"medium\", and \"high\" are allowed. An\n  # empty string is treated as \"medium\".\n  preference = \"medium\"\n\n  # Specifies the lifetime of this prefix. By default, the lifetime is 24 hours.\n  # \"auto\" uses the defaults. \"infinite\" means this route should be used forever.\n  lifetime = \"auto\"\n\n  # RDNSS: attaches a NDP Recursive DNS Servers option to the router advertisement.\n  [[interfaces.rdnss]]\n  # The maximum time these RDNSS addresses may be used for name resolution.\n  # An empty string or 0 means these servers should no longer be used.\n  # \"auto\" will compute a sane default. \"infinite\" means these servers should\n  # be used forever.\n  lifetime = \"auto\"\n  servers = [\"2001:db8::1\", \"2001:db8::2\"]\n\n  # DNSSL: attaches a NDP DNS Search List option to the router advertisement.\n  [[interfaces.dnssl]]\n  # The maximum time these DNSSL domain names may be used for name resolution.\n  # An empty string or 0 means these search domains should no longer be used.\n  # \"auto\" will compute a sane default. \"infinite\" means these search domains\n  # should be used forever.\n  lifetime = \"auto\"\n  domain_names = [\"foo.example.com\"]\n\n# Enable or disable the debug HTTP server for facilities such as Prometheus\n# metrics and pprof support.\n#\n# Warning: do not expose pprof on an untrusted network!\n[debug]\naddress = \"localhost:9430\"\nprometheus = false\npprof = false\n"

// A file is the raw top-level configuration file representation.
type file struct {
	Interfaces []rawInterface `toml:"interfaces"`
	Debug      Debug          `toml:"debug"`
}

// A rawInterface is the raw configuration file representation of an Interface.
type rawInterface struct {
	// Base interface configuration.
	Name            string  `toml:"name"`
	Advertise       bool    `toml:"advertise"`
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
	Prefixes  []rawPrefix `toml:"prefix"`
	Routes    []rawRoute  `toml:"route"`
	RDNSS     []rawRDNSS  `toml:"rdnss"`
	DNSSL     []rawDNSSL  `toml:"dnssl"`
	MTU       int         `toml:"mtu"`
	SourceLLA *bool       `toml:"source_lla"`
}

// A rawPrefix is the raw configuration file representation of a Prefix plugin.
type rawPrefix struct {
	Prefix            string  `toml:"prefix"`
	OnLink            *bool   `toml:"on_link"`
	Autonomous        *bool   `toml:"autonomous"`
	ValidLifetime     *string `toml:"valid_lifetime"`
	PreferredLifetime *string `toml:"preferred_lifetime"`
}

// A rawRoute is the raw configuration file representation of a Route plugin.
type rawRoute struct {
	Prefix     string  `toml:"prefix"`
	Preference string  `toml:"preference"`
	Lifetime   *string `toml:"lifetime"`
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
	Advertise                      bool
	MinInterval, MaxInterval       time.Duration
	Managed, OtherConfig           bool
	ReachableTime, RetransmitTimer time.Duration
	HopLimit                       uint8
	DefaultLifetime                time.Duration
	UnicastOnly                    bool
	Preference                     ndp.Preference
	Plugins                        []plugin.Plugin
}

// Debug provides configuration for debugging and observability.
type Debug struct {
	Address    string `toml:"address"`
	Prometheus bool   `toml:"prometheus"`
	PProf      bool   `toml:"pprof"`
}

// Parse parses a Config in TOML format from an io.Reader and verifies that
// the configuration is valid.
func Parse(r io.Reader) (*Config, error) {
	var f file
	md, err := toml.DecodeReader(r, &f)
	if err != nil {
		return nil, err
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("unrecognized configuration keys: %s", u)
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

	// Don't bother to check for valid interface names; that is more easily
	// done when trying to create server listeners.
	for i, ifi := range f.Interfaces {
		if ifi.Name == "" {
			return nil, fmt.Errorf("interface %d: empty interface name", i)
		}

		iface, err := parseInterface(ifi)
		if err != nil {
			// Narrow down the location of a configuration error.
			return nil, fmt.Errorf("interface %d/%q: %v", i, ifi.Name, err)
		}

		c.Interfaces = append(c.Interfaces, *iface)
	}

	return c, nil
}

// durationAuto implies that a value should be automatically computed.
const durationAuto = -1 * time.Second

// parseDuration parses a duration while also recognizing special values such
// as auto and infinite.
func parseDuration(s *string) (time.Duration, error) {
	if s == nil {
		return durationAuto, nil
	}

	switch *s {
	case "infinite":
		return ndp.Infinity, nil
	case "auto":
		return durationAuto, nil
	case "":
		return 0, nil
	}

	// Use the user's value, but validate it per the RFC.
	return time.ParseDuration(*s)
}
