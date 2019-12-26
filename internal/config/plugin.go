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

	"github.com/BurntSushi/toml"
)

// A Plugin specifies a CoreRAD plugin's configuration.
type Plugin interface {
	// Name is the string name of the plugin.
	Name() string

	// Decode decodes raw TOML configuration into a Plugin's specific
	// configuration.
	Decode(md toml.MetaData, m map[string]toml.Primitive) error
}

// parsePlugin parses raw plugin key/values into a Plugin.
func parsePlugin(md toml.MetaData, m map[string]toml.Primitive) (Plugin, error) {
	// Each plugin is identified by a name.
	pname, ok := m["name"]
	if !ok {
		return nil, errors.New(`missing "name" key for plugin`)
	}

	var name string
	if err := md.PrimitiveDecode(pname, &name); err != nil {
		return nil, err
	}

	// Now that we know the plugin's name, we can initialize the specific Plugin
	// required and decode its individual configuration.
	var p Plugin
	switch name {
	case "prefix":
		p = new(Prefix)
	default:
		return nil, fmt.Errorf("unknown plugin %q", name)
	}

	if err := p.Decode(md, m); err != nil {
		return nil, fmt.Errorf("failed to configure plugin %q: %v", p.Name(), err)
	}

	return p, nil
}

// A Prefix configures a NDP Prefix Information option.
type Prefix struct {
	Prefix     *net.IPNet
	OnLink     *bool
	Autonomous *bool
}

// Name implements Plugin.
func (p *Prefix) Name() string { return "prefix" }

// Decode implements Plugin.
func (p *Prefix) Decode(md toml.MetaData, m map[string]toml.Primitive) error {
	for k := range m {
		var v value
		if err := md.PrimitiveDecode(m[k], &v.v); err != nil {
			return err
		}

		switch k {
		case "name":
			// Already handled.
		case "autonomous":
			b := v.Bool()
			p.Autonomous = &b
		case "on_link":
			b := v.Bool()
			p.OnLink = &b
		case "prefix":
			p.Prefix = v.IPNet()
		default:
			return fmt.Errorf("invalid key %q", k)
		}

		if err := v.Err(); err != nil {
			return fmt.Errorf("parsing key %q: %v", k, err)
		}
	}

	return nil
}
