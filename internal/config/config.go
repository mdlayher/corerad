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

	"github.com/BurntSushi/toml"
)

//go:generate embed file -var Default --source default.toml

// Default is the toml representation of the default configuration.
var Default = "# CoreRAD vALPHA configuration file\n\n# Interfaces which will be used to serve IPv6 NDP router advertisements.\n[[interfaces]]\nname = \"eth0\"\n# AdvSendAdvertisements: indicates whether or not this interface will send\n# periodic router advertisements and respond to router solicitations.\nsend_advertisements = true\n\n  # Zero or more plugins may be specified to modify the behavior of the router\n  # advertisements produced by CoreRAD.\n\n  # \"prefix\" plugin: attaches a NDP Prefix Information option to the router\n  # advertisement.\n  [[interfaces.plugins]]\n  name = \"prefix\"\n  # Serve Prefix Information options for each IPv6 prefix on this interface\n  # configured with a /64 CIDR mask. Any further options specified for this\n  # prefix plugin will be treated as defaults, but can be overridden for\n  # individual prefixes if more prefix plugins are configured.\n  prefix = \"::/64\"\n  on_link = true\n\n  # Serve a prefix with an explicit configuration that may override the defaults\n  # set by ::/64 above.\n  [[interfaces.plugins]]\n  name = \"prefix\"\n  prefix = \"2001:db8::/64\"\n  autonomous = true\n\n# Enable or disable the debug HTTP server for facilities such as Prometheus\n# metrics and pprof support.\n#\n# Warning: do not expose pprof on an untrusted network!\n[debug]\naddress = \"localhost:9430\"\nprometheus = true\npprof = false\n"

// A file is the raw top-level configuration file representation.
type file struct {
	Interfaces []struct {
		Name               string                      `toml:"name"`
		SendAdvertisements bool                        `toml:"send_advertisements"`
		Plugins            []map[string]toml.Primitive `toml:"plugins"`
	} `toml:"interfaces"`
	Debug Debug `toml:"debug"`
}

// Config specifies the configuration for CoreRAD.
type Config struct {
	Interfaces []Interface
	Debug      Debug
}

// An Interface provides configuration for an individual interface.
type Interface struct {
	Name               string
	SendAdvertisements bool
	Plugins            []Plugin
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

		plugins := make([]Plugin, 0, len(ifi.Plugins))
		for j, p := range ifi.Plugins {
			// Narrow down the location of a configuration error.
			handle := func(err error) error {
				return fmt.Errorf("interface %d/%q, plugin %d: %v", i, ifi.Name, j, err)
			}

			plug, err := parsePlugin(md, p)
			if err != nil {
				return nil, handle(err)
			}

			plugins = append(plugins, plug)
		}

		c.Interfaces = append(c.Interfaces, Interface{
			Name:               ifi.Name,
			SendAdvertisements: ifi.SendAdvertisements,
			Plugins:            plugins,
		})
	}

	return c, nil
}
