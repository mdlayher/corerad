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

package metrics_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/metrics"
)

func TestMemory(t *testing.T) {
	tests := []struct {
		name  string
		fn    func(m *metrics.Memory)
		final map[string]metrics.Series
	}{
		{
			name:  "noop",
			final: map[string]metrics.Series{},
		},
		{
			name: "counters",
			fn: func(m *metrics.Memory) {
				var (
					c1 = m.Counter("foo_total", "A counter.", "address", "interface")
					c2 = m.Counter("bar_total", "A second counter.")
				)

				// A counter increments for each call with a given label set.
				c1("127.0.0.1", "eth0")
				c1("::1", "eth0")
				c1("2001:db8::1", "eth1")

				for i := 0; i < 5; i++ {
					c2()
				}
			},
			final: map[string]metrics.Series{
				"bar_total": {
					Name:    "bar_total",
					Help:    "A second counter.",
					Samples: map[string]float64{"": 5},
				},
				"foo_total": {
					Name: "foo_total",
					Help: "A counter.",
					Samples: map[string]float64{
						"address=127.0.0.1,interface=eth0":   1,
						"address=2001:db8::1,interface=eth1": 1,
						"address=::1,interface=eth0":         1,
					},
				},
			},
		},
		{
			name: "gauges",
			fn: func(m *metrics.Memory) {
				var (
					g1 = m.Gauge("foo_celsius", "A gauge.", "probe")
					g2 = m.Gauge("bar_bytes", "A second gauge.", "device")
				)

				// A gauge only stores the last value for a given timeseries.
				g1(0, "temp0")
				g1(100, "temp1")
				g1(1, "temp0")

				g2(1024, "eth0")
			},
			final: map[string]metrics.Series{
				"bar_bytes": {
					Name:    "bar_bytes",
					Help:    "A second gauge.",
					Samples: map[string]float64{"device=eth0": 1024},
				},
				"foo_celsius": {
					Name: "foo_celsius",
					Help: "A gauge.",
					Samples: map[string]float64{
						"probe=temp0": 1,
						"probe=temp1": 100,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := metrics.NewMemory()

			if tt.fn != nil {
				tt.fn(m)
			}

			if diff := cmp.Diff(tt.final, m.Series()); diff != "" {
				t.Fatalf("unexpected timeseries (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMemoryPanics(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		fn   func(m *metrics.Memory)
	}{
		{
			name: "already registered",
			msg:  `metrics: timeseries "foo_total" already registered`,
			fn: func(m *metrics.Memory) {
				m.Counter("foo_total", "A counter.")
				m.Gauge("foo_total", "A gauge.")
			},
		},
		{
			name: "mismatched cardinality",
			msg:  `metrics: mismatched label cardinality for timeseries "foo_total"`,
			fn: func(m *metrics.Memory) {
				c := m.Counter("foo_total", "A counter.")
				c("panics")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, panics := panics(func() {
				tt.fn(metrics.NewMemory())
			})
			if !panics {
				t.Fatal("test did not panic")
			}

			if diff := cmp.Diff(tt.msg, msg); diff != "" {
				t.Fatalf("unexpected panic message (-want +got):\n%s", diff)
			}
		})
	}
}

func panics(fn func()) (msg string, panics bool) {
	defer func() {
		if r := recover(); r != nil {
			// On panic, capture the output string and return that fn did
			// panic.
			msg = r.(string)
			panics = true
		}
	}()

	fn()

	// fn did not panic.
	return "", false
}
