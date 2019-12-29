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
