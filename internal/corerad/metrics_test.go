// Copyright 2019-2022 Matt Layher
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
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/metricslite"
)

func TestMetrics(t *testing.T) {
	var (
		// Server-wide timeseries which are always set regardless of
		// configuration. Hard-coded values are passed in the test table.
		base = map[string]metricslite.Series{
			"corerad_build_info":              {Samples: map[string]float64{"version=test": 1}},
			"corerad_build_timestamp_seconds": {Samples: map[string]float64{"": 0}},
		}

		// All interfaces except unused are assumed to be forwarding traffic.
		state = system.TestState{
			Forwarding: true,
			Interfaces: map[string]system.TestStateInterface{
				"eth2": {Forwarding: false},
			},
		}

		// A WAN interface which is configured to monitor but not advertise.
		wan = map[string]metricslite.Series{
			ifiAdvertising:       {Samples: map[string]float64{"interface=eth0": 0}},
			ifiAutoconfiguration: {Samples: map[string]float64{"interface=eth0": 0}},
			ifiForwarding:        {Samples: map[string]float64{"interface=eth0": 1}},
			ifiMonitoring:        {Samples: map[string]float64{"interface=eth0": 1}},
		}

		// A LAN interface which is configured to advertise but not monitor.
		lan = map[string]metricslite.Series{
			ifiAdvertising:       {Samples: map[string]float64{"interface=eth1": 1}},
			ifiAutoconfiguration: {Samples: map[string]float64{"interface=eth1": 0}},
			ifiForwarding:        {Samples: map[string]float64{"interface=eth1": 1}},
			ifiMonitoring:        {Samples: map[string]float64{"interface=eth1": 0}},
		}

		// An unused interface which is in the config but neither advertising
		// nor monitoring.
		unused = map[string]metricslite.Series{
			ifiAdvertising:       {Samples: map[string]float64{"interface=eth2": 0}},
			ifiAutoconfiguration: {Samples: map[string]float64{"interface=eth2": 0}},
			ifiForwarding:        {Samples: map[string]float64{"interface=eth2": 0}},
			ifiMonitoring:        {Samples: map[string]float64{"interface=eth2": 0}},
		}
	)

	tests := []struct {
		name   string
		ts     system.TestState
		ifis   []config.Interface
		series map[string]metricslite.Series
	}{
		{
			name:   "no interfaces",
			series: base,
		},
		{
			name: "interface with errors",
			ifis: []config.Interface{{Name: "eth0"}},
			ts:   system.TestState{Error: errors.New("some error")},
			series: mergeSeries(base, map[string]metricslite.Series{
				ifiForwarding: {Samples: map[string]float64{"": -1}},
			}),
		},
		{
			name:   "interface not configured",
			ts:     state,
			ifis:   []config.Interface{{Name: "eth2"}},
			series: mergeSeries(base, unused),
		},
		{
			name: "interfaces monitoring and advertising",
			ts:   state,
			ifis: []config.Interface{
				{
					Name:    "eth0",
					Monitor: true,
				},
				{
					Name:      "eth1",
					Advertise: true,
					Plugins: []plugin.Plugin{
						&plugin.Prefix{Prefix: netip.MustParsePrefix("2001:db8::/64")},
						&plugin.Prefix{
							Prefix:            netip.MustParsePrefix("fdff:dead:beef:dead::/64"),
							Autonomous:        true,
							OnLink:            true,
							ValidLifetime:     20 * time.Minute,
							PreferredLifetime: 10 * time.Minute,
						},
					},
				},
			},
			series: mergeSeries(base, wan, lan, map[string]metricslite.Series{
				advPrefixAutonomous: {
					Samples: map[string]float64{
						"interface=eth1,prefix=2001:db8::/64":            0,
						"interface=eth1,prefix=fdff:dead:beef:dead::/64": 1,
					},
				},
				advPrefixOnLink: {
					Samples: map[string]float64{
						"interface=eth1,prefix=2001:db8::/64":            0,
						"interface=eth1,prefix=fdff:dead:beef:dead::/64": 1,
					},
				},
				advPrefixValid: {
					Samples: map[string]float64{
						"interface=eth1,prefix=2001:db8::/64":            0,
						"interface=eth1,prefix=fdff:dead:beef:dead::/64": 1200,
					},
				},
				advPrefixPreferred: {
					Samples: map[string]float64{
						"interface=eth1,prefix=2001:db8::/64":            0,
						"interface=eth1,prefix=fdff:dead:beef:dead::/64": 600,
					},
				},
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mm := NewMetrics(
				metricslite.NewMemory(),
				"test",
				time.Time{},
				tt.ts,
				tt.ifis,
			)

			raw, ok := mm.Series()
			if !ok {
				t.Fatalf("type %T does not support fetching timeseries", mm)
			}

			// Skip empty timeseries for output comparison, and remove name
			// (redundant with key) and help text to make fixtures more concise.
			series := make(map[string]metricslite.Series)
			for k, v := range raw {
				if len(v.Samples) > 0 {
					v.Name = ""
					v.Help = ""
					series[k] = v
				}
			}

			if diff := cmp.Diff(tt.series, series); diff != "" {
				t.Fatalf("unexpected timeseries (-want +got):\n%s", diff)
			}
		})
	}
}

// mergeSeries allows merging multiple timeseries maps into a single one.
func mergeSeries(series ...map[string]metricslite.Series) map[string]metricslite.Series {
	out := make(map[string]metricslite.Series)
	for _, s := range series {
		for k, v := range s {
			if _, ok := out[k]; !ok {
				// New timeseries, prepare the output map.
				out[k] = metricslite.Series{Samples: make(map[string]float64)}
			}

			// Skip name and help since they're ignored, just merge samples.
			for kk, vv := range v.Samples {
				out[k].Samples[kk] = vv
			}
		}
	}

	return out
}
