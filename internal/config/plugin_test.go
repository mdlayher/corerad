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

package config_test

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
)

func TestPrefixDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		p    *config.Prefix
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
			name: "bad prefix",
			s: `
			name = "prefix"
			prefix = "foo"
			`,
		},
		{
			name: "OK",
			s: `
			name = "prefix"
			prefix = "::/64"
			autonomous = false
			on_link = true
			`,
			p: &config.Prefix{
				Prefix:     mustCIDR("::/64"),
				Autonomous: boolp(false),
				OnLink:     boolp(true),
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m map[string]toml.Primitive

			md, err := toml.DecodeReader(strings.NewReader(tt.s), &m)
			if err != nil {
				t.Fatalf("failed to decode TOML: %v", err)
			}

			p := new(config.Prefix)
			err = p.Decode(md, m)
			if tt.ok && err != nil {
				t.Fatalf("failed to decode Prefix: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				t.Logf("err: %v", err)
				return
			}

			if diff := cmp.Diff(tt.p, p); diff != "" {
				t.Fatalf("unexpected Prefix (-want +got):\n%s", diff)
			}
		})
	}
}

func boolp(v bool) *bool { return &v }
