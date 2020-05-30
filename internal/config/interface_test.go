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

import "testing"

func Test_parseInterfaceErrors(t *testing.T) {
	tests := []struct {
		name string
		ifi  rawInterface
	}{
		{
			name: "monitor and advertise",
			ifi: rawInterface{
				Monitor:   true,
				Advertise: true,
			},
		},
		{
			name: "max interval duration",
			ifi: rawInterface{
				MaxInterval: "foo",
			},
		},
		{
			name: "max interval too low",
			ifi: rawInterface{
				MaxInterval: "1s",
			},
		},
		{
			name: "max interval too high",
			ifi: rawInterface{
				MaxInterval: "24h",
			},
		},
		{
			name: "min interval duration",
			ifi: rawInterface{
				MinInterval: "foo",
			},
		},
		{
			name: "min interval too low",
			ifi: rawInterface{
				MinInterval: "1s",
			},
		},
		{
			name: "min interval too high",
			ifi: rawInterface{
				MinInterval: "4s",
				MaxInterval: "4s",
			},
		},
		{
			name: "reachable time duration",
			ifi: rawInterface{
				ReachableTime: "foo",
			},
		},
		{
			name: "reachable time too low",
			ifi: rawInterface{
				ReachableTime: "-1s",
			},
		},
		{
			name: "reachable time too high",
			ifi: rawInterface{
				ReachableTime: "9000s",
			},
		},
		{
			name: "retransmit timer duration",
			ifi: rawInterface{
				RetransmitTimer: "foo",
			},
		},
		{
			name: "retransmit timer too low",
			ifi: rawInterface{
				RetransmitTimer: "-1s",
			},
		},
		{
			name: "retransmit timer too high",
			ifi: rawInterface{
				RetransmitTimer: "9000s",
			},
		},
		{
			name: "hop limit too low",
			ifi: rawInterface{
				HopLimit: intp(-1),
			},
		},
		{
			name: "hop limit too high",
			ifi: rawInterface{
				HopLimit: intp(256),
			},
		},
		{
			name: "default lifetime duration",
			ifi: rawInterface{
				DefaultLifetime: strp("foo"),
			},
		},
		{
			name: "default lifetime too low",
			ifi: rawInterface{
				MaxInterval:     "4s",
				DefaultLifetime: strp("2s"),
			},
		},
		{
			name: "default lifetime too high",
			ifi: rawInterface{
				DefaultLifetime: strp("9001s"),
			},
		},
		{
			name: "preference invalid",
			ifi: rawInterface{
				Preference: "foo",
			},
		},
		{
			name: "MTU too low",
			ifi: rawInterface{
				MTU: -1,
			},
		},
		{
			name: "MTU too high",
			ifi: rawInterface{
				MTU: 65537,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseInterface(tt.ifi)
			if err == nil {
				t.Fatal("expected an error, but none occurred")
			}

			t.Logf("err: %v", err)
		})
	}
}

func intp(i int) *int { return &i }

func strp(s string) *string { return &s }
