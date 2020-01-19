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
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/ndp"
)

// Tests in this file use a greatly reduced config to test plugin parsing edge
// cases. The config as a whole is not expected to be valid.

func Test_parseDNSSL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		d    *plugin.DNSSL
		ok   bool
	}{
		{
			name: "bad lifetime",
			s: `
			[[interfaces]]
			  [[interfaces.dnssl]]
			  lifetime = "foo"
			`,
		},
		{
			name: "bad domain names",
			s: `
			[[interfaces]]
			  [[interfaces.dnssl]]
			  domain_names = []
			`,
		},
		{
			name: "OK explicit",
			s: `
			[[interfaces]]
			  [[interfaces.dnssl]]
			  domain_names = ["foo.example.com", "bar.example.com"]
			  lifetime = "30s"
			`,
			d: &plugin.DNSSL{
				Lifetime:    30 * time.Second,
				DomainNames: []string{"foo.example.com", "bar.example.com"},
			},
			ok: true,
		},
		{
			name: "OK implicit",
			s: `
			[[interfaces]]
			  [[interfaces.dnssl]]
			  domain_names = ["foo.example.com"]
			`,
			d: &plugin.DNSSL{
				Lifetime:    20 * time.Minute,
				DomainNames: []string{"foo.example.com"},
			},
			ok: true,
		},
		{
			name: "OK auto",
			s: `
			[[interfaces]]
			  [[interfaces.dnssl]]
			  domain_names = ["foo.example.com"]
			  lifetime = "auto"
			`,
			d: &plugin.DNSSL{
				Lifetime:    20 * time.Minute,
				DomainNames: []string{"foo.example.com"},
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginDecode(t, tt.s, tt.ok, tt.d)
		})
	}
}

func Test_parseMTU(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		m    *plugin.MTU
		ok   bool
	}{
		{
			name: "too low",
			s: `
			[[interfaces]]
			mtu = -1
			`,
		},
		{
			name: "too high",
			s: `
			[[interfaces]]
			mtu = 999999
			`,
		},
		{
			name: "OK",
			s: `
			[[interfaces]]
			mtu = 1500
			`,
			m:  plugin.NewMTU(1500),
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginDecode(t, tt.s, tt.ok, tt.m)
		})
	}
}

func Test_parsePrefix(t *testing.T) {
	t.Parallel()

	defaults := &plugin.Prefix{
		Prefix:            mustCIDR("::/64"),
		OnLink:            true,
		Autonomous:        true,
		PreferredLifetime: 4 * time.Hour,
		ValidLifetime:     24 * time.Hour,
	}

	tests := []struct {
		name string
		s    string
		p    *plugin.Prefix
		ok   bool
	}{
		{
			name: "no prefix",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			`,
		},
		{
			name: "bad prefix string",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "foo"
			`,
		},
		{
			name: "bad prefix individual IP",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::1/64"
			`,
		},
		{
			name: "bad prefix IPv4",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "192.0.2.0/24"
			`,
		},
		{
			name: "bad prefix ::/N",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/63"
			`,
		},
		{
			name: "bad valid lifetime string",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			  valid_lifetime = "foo"
			`,
		},
		{
			name: "bad valid lifetime zero",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			  valid_lifetime = ""
			`,
		},
		{
			name: "bad preferred lifetime string",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			  preferred_lifetime = "foo"
			  valid_lifetime = "2s"
			`,
		},
		{
			name: "bad preferred lifetime zero",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			  preferred_lifetime = ""
			  valid_lifetime = "2s"
			`,
		},
		{
			name: "bad lifetimes",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			  preferred_lifetime = "2s"
			  valid_lifetime = "1s"
			`,
		},
		{
			name: "OK defaults",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			`,
			p:  defaults,
			ok: true,
		},
		{
			name: "OK auto durations",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			  preferred_lifetime = "auto"
			  valid_lifetime = "auto"
			`,
			p:  defaults,
			ok: true,
		},
		{
			name: "OK infinite durations",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			  preferred_lifetime = "infinite"
			  valid_lifetime = "infinite"
			`,
			p: &plugin.Prefix{
				Prefix:            mustCIDR("::/64"),
				OnLink:            true,
				Autonomous:        true,
				PreferredLifetime: ndp.Infinity,
				ValidLifetime:     ndp.Infinity,
			},
			ok: true,
		},
		{
			name: "OK explicit",
			s: `
			[[interfaces]]
			  [[interfaces.prefix]]
			  prefix = "::/64"
			  autonomous = false
			  on_link = true
			  preferred_lifetime = "30s"
			  valid_lifetime = "60s"
			`,
			p: &plugin.Prefix{
				Prefix:            mustCIDR("::/64"),
				OnLink:            true,
				PreferredLifetime: 30 * time.Second,
				ValidLifetime:     60 * time.Second,
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginDecode(t, tt.s, tt.ok, tt.p)
		})
	}
}

func Test_parseRDNSS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		r    *plugin.RDNSS
		ok   bool
	}{
		{
			name: "bad lifetime",
			s: `
			[[interfaces]]
			  [[interfaces.rdnss]]
			  lifetime = "foo"
			`,
		},
		{
			name: "bad servers empty",
			s: `
			[[interfaces]]
			  [[interfaces.rdnss]]
			  servers = []
			`,
		},
		{
			name: "bad servers IPv4",
			s: `
			[[interfaces]]
			  [[interfaces.rdnss]]
			  servers = ["192.0.2.1"]
			`,
		},
		{
			name: "OK explicit",
			s: `
			[[interfaces]]
			  [[interfaces.rdnss]]
			  servers = ["2001:db8::1", "2001:db8::2"]
			  lifetime = "30s"
			`,
			r: &plugin.RDNSS{
				Lifetime: 30 * time.Second,
				Servers: []net.IP{
					mustIP("2001:db8::1"),
					mustIP("2001:db8::2"),
				},
			},
			ok: true,
		},
		{
			name: "OK implicit",
			s: `
			[[interfaces]]
			  [[interfaces.rdnss]]
			  servers = ["2001:db8::1"]
			`,
			r: &plugin.RDNSS{
				Lifetime: 20 * time.Minute,
				Servers:  []net.IP{mustIP("2001:db8::1")},
			},
			ok: true,
		},
		{
			name: "OK auto",
			s: `
			[[interfaces]]
			  [[interfaces.rdnss]]
			  servers = ["2001:db8::1"]
			  lifetime = "auto"
			`,
			r: &plugin.RDNSS{
				Lifetime: 20 * time.Minute,
				Servers:  []net.IP{mustIP("2001:db8::1")},
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginDecode(t, tt.s, tt.ok, tt.r)
		})
	}
}

func pluginDecode(t *testing.T, s string, ok bool, want plugin.Plugin) {
	t.Helper()

	var f file
	if _, err := toml.DecodeReader(strings.NewReader(s), &f); err != nil {
		t.Fatalf("failed to decode TOML: %v", err)
	}
	if l := len(f.Interfaces); l != 1 {
		t.Fatalf("expected one configured interface, but got: %d", l)
	}

	// For test purposes, only attach source LLA if explicitly true.
	if f.Interfaces[0].SourceLLA == nil {
		v := false
		f.Interfaces[0].SourceLLA = &v
	}

	// Defaults used when computing automatic values.
	const maxInterval = 10 * time.Minute

	got, err := parsePlugins(f.Interfaces[0], maxInterval)
	if ok && err != nil {
		t.Fatalf("failed to parse Plugin: %v", err)
	}
	if !ok && err == nil {
		t.Fatal("expected an error, but none occurred")
	}
	if err != nil {
		t.Logf("err: %v", err)
		return
	}

	if diff := cmp.Diff([]plugin.Plugin{want}, got); diff != "" {
		t.Fatalf("unexpected Plugin (-want +got):\n%s", diff)
	}
}

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panicf("failed to parse %q as IP address", s)
	}

	return ip
}

func mustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
