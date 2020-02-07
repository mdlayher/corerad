// Copyright 2020 Matt Layher
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

package corerad

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/ndp"
)

func Test_verifyRAs(t *testing.T) {
	prefix := []ndp.Option{
		&ndp.PrefixInformation{
			Prefix:            mustIP("2001:db8::"),
			PrefixLength:      64,
			PreferredLifetime: 1 * time.Second,
			ValidLifetime:     2 * time.Second,
		},
	}

	full := &ndp.RouterAdvertisement{
		CurrentHopLimit:      64,
		ManagedConfiguration: true,
		OtherConfiguration:   true,
		ReachableTime:        30 * time.Minute,
		RetransmitTimer:      60 * time.Minute,
		Options: []ndp.Option{
			// Use prefix from above.
			prefix[0],
			&ndp.PrefixInformation{
				PrefixLength:      32,
				OnLink:            true,
				PreferredLifetime: 10 * time.Second,
				ValidLifetime:     20 * time.Second,
				Prefix:            mustIP("fdff:dead:beef::"),
			},
			ndp.NewMTU(1500),
		},
	}

	tests := []struct {
		name string
		a, b *ndp.RouterAdvertisement
		ok   bool
	}{
		{
			name: "hop limit",
			a:    &ndp.RouterAdvertisement{CurrentHopLimit: 1},
			b:    &ndp.RouterAdvertisement{CurrentHopLimit: 2},
		},
		{
			name: "managed",
			a:    &ndp.RouterAdvertisement{ManagedConfiguration: true},
			b:    &ndp.RouterAdvertisement{ManagedConfiguration: false},
		},
		{
			name: "other",
			a:    &ndp.RouterAdvertisement{OtherConfiguration: true},
			b:    &ndp.RouterAdvertisement{OtherConfiguration: false},
		},
		{
			name: "reachable time",
			a:    &ndp.RouterAdvertisement{ReachableTime: 1 * time.Second},
			b:    &ndp.RouterAdvertisement{ReachableTime: 2 * time.Second},
		},
		{
			name: "retransmit timer",
			a:    &ndp.RouterAdvertisement{RetransmitTimer: 1 * time.Second},
			b:    &ndp.RouterAdvertisement{RetransmitTimer: 2 * time.Second},
		},
		{
			name: "MTU",
			a: &ndp.RouterAdvertisement{
				Options: []ndp.Option{ndp.NewMTU(1500)},
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{ndp.NewMTU(9000)},
			},
		},
		{
			name: "prefix",
			a: &ndp.RouterAdvertisement{
				Options: prefix,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						Prefix:            mustIP("2001:db8::"),
						PrefixLength:      64,
						PreferredLifetime: 3 * time.Second,
						ValidLifetime:     4 * time.Second,
					},
				},
			},
		},
		{
			name: "OK, reachable time unspecified",
			a:    &ndp.RouterAdvertisement{},
			b:    &ndp.RouterAdvertisement{ReachableTime: 1 * time.Second},
			ok:   true,
		},
		{
			name: "OK, retransmit timer unspecified",
			a:    &ndp.RouterAdvertisement{},
			b:    &ndp.RouterAdvertisement{RetransmitTimer: 1 * time.Second},
			ok:   true,
		},
		{
			name: "OK, MTU unspecified",
			a: &ndp.RouterAdvertisement{
				Options: []ndp.Option{ndp.NewMTU(1500)},
			},
			b:  &ndp.RouterAdvertisement{},
			ok: true,
		},
		{
			name: "OK, prefix unspecified",
			a: &ndp.RouterAdvertisement{
				Options: prefix,
			},
			b:  &ndp.RouterAdvertisement{},
			ok: true,
		},
		{
			name: "OK, prefix different",
			a: &ndp.RouterAdvertisement{
				Options: prefix,
			},
			b: &ndp.RouterAdvertisement{
				Options: []ndp.Option{
					&ndp.PrefixInformation{
						Prefix:            mustIP("fdff:dead:beef::"),
						PrefixLength:      64,
						PreferredLifetime: 3 * time.Second,
						ValidLifetime:     4 * time.Second,
					},
				},
			},
			ok: true,
		},
		{
			name: "OK, all",
			a:    full,
			b:    full,
			ok:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.ok, verifyRAs(tt.a, tt.b)); diff != "" {
				t.Fatalf("unexpected router advertisement consistency (-want +got):\n%s", diff)
			}
		})
	}
}
