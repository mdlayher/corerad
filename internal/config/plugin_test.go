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
	"net"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/ndp"
)

func Test_parseDNSSL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		d    *plugin.DNSSL
		ok   bool
	}{
		{
			name: "unknown key",
			s: `
			name = "dnssl"
			bad = true
			`,
		},
		{
			name: "bad lifetime",
			s: `
			name = "dnssl"
			lifetime = "foo"
			`,
		},
		{
			name: "bad domain names",
			s: `
			name = "dnssl"
			domain_names = [1]
			`,
		},
		{
			name: "OK explicit",
			s: `
			name = "dnssl"
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
			name = "dnssl"
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
			name = "dnssl"
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
			name: "unknown key",
			s: `
			name = "prefix"
			bad = true
			`,
		},
		{
			name: "no prefix",
			s: `
			name = "prefix"
			`,
		},
		{
			name: "bad prefix",
			s: `
			name = "prefix"
			prefix = "foo"
			`,
		},
		{
			name: "bad valid lifetime",
			s: `
			name = "prefix"
			prefix = "::/64"
			preferred_lifetime = "2s"
			valid_lifetime = ""
			`,
		},
		{
			name: "bad preferred lifetime",
			s: `
			name = "prefix"
			prefix = "::/64"
			preferred_lifetime = ""
			valid_lifetime = "2s"
			`,
		},
		{
			name: "bad lifetimes",
			s: `
			name = "prefix"
			prefix = "::/64"
			preferred_lifetime = "2s"
			valid_lifetime = "1s"
			`,
		},
		{
			name: "OK defaults",
			s: `
			name = "prefix"
			prefix = "::/64"
			`,
			p:  defaults,
			ok: true,
		},
		{
			name: "OK auto durations",
			s: `
			name = "prefix"
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
			name = "prefix"
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
			name = "prefix"
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
			name: "unknown key",
			s: `
			name = "rdnss"
			bad = true
			`,
		},
		{
			name: "bad lifetime",
			s: `
			name = "rdnss"
			lifetime = "foo"
			`,
		},
		{
			name: "bad servers",
			s: `
			name = "rdnss"
			servers = ["192.0.2.1"]
			`,
		},
		{
			name: "OK explicit",
			s: `
			name = "rdnss"
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
			name = "rdnss"
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
			name = "rdnss"
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

	var m map[string]toml.Primitive

	md, err := toml.DecodeReader(strings.NewReader(s), &m)
	if err != nil {
		t.Fatalf("failed to decode TOML: %v", err)
	}

	// Defaults used when computing automatic values.
	iface := Interface{MaxInterval: 10 * time.Minute}

	got, err := parsePlugin(iface, md, m)
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

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unexpected Plugin (-want +got):\n%s", diff)
	}
}
