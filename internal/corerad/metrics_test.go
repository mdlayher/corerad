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

package corerad

import (
	"fmt"
	"net"
	"testing"

	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/promtest"
)

func Test_interfaceCollector(t *testing.T) {
	// This test probably only works on Linux, so skip early if need be.
	loop, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skipf("skipping, failed to get loopback interface: %v", err)
	}

	// Fake public keys used to identify devices and peers.
	tests := []struct {
		name    string
		ifis    []config.Interface
		metrics []string
	}{
		{
			name: "ok",
			ifis: []config.Interface{{
				Name:               loop.Name,
				SendAdvertisements: true,
			}},
			metrics: []string{
				fmt.Sprintf(`corerad_interface_info{autoconfiguration="true",forwarding="false",interface="%s",send_advertisements="true"} 1`, loop.Name),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := promtest.Collect(t, newInterfaceCollector(tt.ifis))

			if !promtest.Lint(t, body) {
				t.Fatal("one or more promlint errors found")
			}

			if !promtest.Match(t, body, tt.metrics) {
				t.Fatal("metrics did not match whitelist")
			}
		})
	}
}
