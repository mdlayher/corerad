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

package metrics

import (
	"fmt"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// A Counter is a function which increments a metric's value by 1 when invoked.
// Labels enable optional partitioning of a Counter into multiple dimensions.
// Counters must be safe for concurrent use.
type Counter func(labels ...string)

// A Gauge is a function which sets a metric's value when invoked.
// Labels enable optional partitioning of a Counter into multiple dimensions.
// Gauges must be safe for concurrent use.
type Gauge func(value float64, labels ...string)

// An Interface is a type which can produce metrics functions. An Interface
// implementation must be safe for concurrent use.
type Interface interface {
	Counter(name, help string, labelNames ...string) Counter
	Gauge(name, help string, labelNames ...string) Gauge
}

// prom implements Interface by wrapping the Prometheus client library.
type prom struct {
	reg *prometheus.Registry
}

var _ Interface = &prom{}

// NewPrometheus creates an Interface which will register all of its metrics
// to the specified Prometheus registry. The registry must not be nil.
func NewPrometheus(reg *prometheus.Registry) Interface {
	return &prom{reg: reg}
}

// Counter implements Interface.
func (p *prom) Counter(name, help string, labelNames ...string) Counter {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: name,
		Help: help,
	}, labelNames)

	p.reg.MustRegister(c)

	return func(labels ...string) {
		c.WithLabelValues(labels...).Inc()
	}
}

// Gauge implements Interface.
func (p *prom) Gauge(name, help string, labelNames ...string) Gauge {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	}, labelNames)

	p.reg.MustRegister(g)

	return func(value float64, labels ...string) {
		g.WithLabelValues(labels...).Set(value)
	}
}

// discard implements Interface by discarding all metrics.
type discard struct{}

var _ Interface = discard{}

// Discard creates an Interface which creates no-op metrics that discard their
// data.
func Discard() Interface { return discard{} }

// Counter implements Interface.
func (discard) Counter(_, _ string, _ ...string) Counter { return func(_ ...string) {} }

// Gauge implements Interface.
func (discard) Gauge(_, _ string, _ ...string) Gauge { return func(_ float64, _ ...string) {} }

// Memory implements Interface by storing timeseries and samples in memory.
// This type is primarily useful for tests.
type Memory struct {
	// Records stores a record of each operation performed on the Recorder.
	mu     sync.Mutex
	series map[string]*Series
}

// Series produces a copy of all of the timeseries and samples stored by
// the Memory.
func (m *Memory) Series() map[string]Series {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[string]Series, len(m.series))
	for k, v := range m.series {
		out[k] = *v
	}

	return out
}

// NewMemory creates an initialized Memory.
func NewMemory() *Memory { return &Memory{series: make(map[string]*Series)} }

// A Series is a timeseries with metadata and samples partitioned by labels.
type Series struct {
	Name, Help string
	Samples    map[string]float64
}

// Counter implements Interface.
func (m *Memory) Counter(name, help string, labelNames ...string) Counter {
	m.mu.Lock()
	defer m.mu.Unlock()

	samples := m.register(name, help, labelNames...)

	return func(labels ...string) {
		m.mu.Lock()
		defer m.mu.Unlock()

		// Counter always increment.
		samples[sampleKVs(name, labelNames, labels)]++
	}
}

// Gauge implements Interface.
func (m *Memory) Gauge(name, help string, labelNames ...string) Gauge {
	m.mu.Lock()
	defer m.mu.Unlock()

	samples := m.register(name, help, labelNames...)

	return func(value float64, labels ...string) {
		m.mu.Lock()
		defer m.mu.Unlock()

		// Gauges set an arbitrary value.
		samples[sampleKVs(name, labelNames, labels)] = value
	}
}

// register registers a timeseries and produces a samples map for that series.
func (m *Memory) register(name, help string, labelNames ...string) map[string]float64 {
	if _, ok := m.series[name]; ok {
		panicf("metrics: timeseries %q already registered", name)
	}

	samples := make(map[string]float64)

	m.series[name] = &Series{
		Name:    name,
		Help:    help,
		Samples: samples,
	}

	return samples
}

// sampleKVs produces a map key for Memory series output.
func sampleKVs(name string, labelNames, labels []string) string {
	// Must have the same argument cardinality.
	if len(labels) != len(labelNames) {
		panicf("metrics: mismatched label cardinality for timeseries %q", name)
	}

	// Join key/values as "key=value,foo=bar".
	kvs := make([]string, 0, len(labels))
	for i := 0; i < len(labels); i++ {
		kvs = append(kvs, strings.Join([]string{labelNames[i], labels[i]}, "="))
	}

	return strings.Join(kvs, ",")
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
