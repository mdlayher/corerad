// Copyright 2020-2022 Matt Layher
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

package system_test

import (
	"errors"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/system"
)

func TestTestState(t *testing.T) {
	tests := []struct {
		name  string
		ts    system.TestState
		check func(t *testing.T, ts system.TestState)
	}{
		{
			name: "no interfaces",
			check: func(t *testing.T, ts system.TestState) {
				auto, err := ts.IPv6Autoconf("eth0")
				if err != nil {
					t.Fatalf("failed to fetch autoconfiguration: %v", err)
				}

				if diff := cmp.Diff(false, auto); diff != "" {
					t.Fatalf("unexpected autoconfiguration (-want +got):\n%s", diff)
				}

				fwd, err := ts.IPv6Forwarding("eth0")
				if err != nil {
					t.Fatalf("failed to fetch autoconfiguration: %v", err)
				}

				if diff := cmp.Diff(false, fwd); diff != "" {
					t.Fatalf("unexpected forwarding (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "global error",
			ts: system.TestState{
				Error: os.ErrPermission,
			},
			check: func(t *testing.T, ts system.TestState) {
				if _, err := ts.IPv6Autoconf("eth0"); !errors.Is(err, os.ErrPermission) {
					t.Fatalf("unexpected autoconfiguration error: %v", err)
				}

				if _, err := ts.IPv6Forwarding("eth0"); !errors.Is(err, os.ErrPermission) {
					t.Fatalf("unexpected forwarding error: %v", err)
				}
			},
		},
		{
			name: "eth0 override",
			ts: system.TestState{
				Interfaces: map[string]system.TestStateInterface{
					"eth0": {Autoconf: true, Forwarding: true},
					"eth1": {Autoconf: false, Forwarding: true},
					// eth2 will use the global configuration of false/false.
				},
			},
			check: func(t *testing.T, ts system.TestState) {
				for _, s := range []string{"eth0", "eth1", "eth2"} {
					auto, err := ts.IPv6Autoconf(s)
					if err != nil {
						t.Fatalf("failed to fetch autoconfiguration: %v", err)
					}

					fwd, err := ts.IPv6Forwarding(s)
					if err != nil {
						t.Fatalf("failed to fetch forwarding: %v", err)
					}

					got := system.TestStateInterface{
						Autoconf:   auto,
						Forwarding: fwd,
					}

					tsi, ok := ts.Interfaces[s]
					if !ok {
						// Not in the map, assume false/false.
						if diff := cmp.Diff(system.TestStateInterface{}, got); diff != "" {
							t.Fatalf("unexpected global interface state (-want +got):\n%s", diff)
						}
						return
					}

					if diff := cmp.Diff(tsi, got); diff != "" {
						t.Fatalf("unexpected per-interface state (-want +got):\n%s", diff)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, tt.ts)
		})
	}
}
