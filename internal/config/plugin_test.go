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
)

func TestPrefixDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		p    *Prefix
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
			name: "bad lifetimes",
			s: `
			name = "prefix"
			prefix = "::/64"
			preferred_lifetime = "2s"
			valid_lifetime = "1s"
			`,
		},
		{
			name: "OK",
			s: `
			name = "prefix"
			prefix = "::/64"
			autonomous = false
			on_link = true
			preferred_lifetime = "30s"
			valid_lifetime = "60s"
			`,
			p: &Prefix{
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

func TestRDNSSDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		r    *RDNSS
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
			name: "OK",
			s: `
			name = "rdnss"
			servers = ["2001:db8::1", "2001:db8::2"]
			lifetime = "30s"
			`,
			r: &RDNSS{
				Lifetime: 30 * time.Second,
				Servers: []net.IP{
					mustIP("2001:db8::1"),
					mustIP("2001:db8::2"),
				},
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

func pluginDecode(t *testing.T, s string, ok bool, want Plugin) {
	t.Helper()

	var m map[string]toml.Primitive

	md, err := toml.DecodeReader(strings.NewReader(s), &m)
	if err != nil {
		t.Fatalf("failed to decode TOML: %v", err)
	}

	got, err := parsePlugin(md, m)
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
		t.Fatalf("unexpected Prefix (-want +got):\n%s", diff)
	}
}
