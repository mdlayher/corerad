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
	"sync"

	"github.com/mdlayher/corerad/internal/build"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics contains metrics for a CoreRAD instance.
type Metrics struct {
	// Server metrics.
	Info *prometheus.GaugeVec
	Time prometheus.Gauge

	// TODO(mdlayher): potentially add metrics for more prefix fields and
	// RDNSS/DNSSL option configurations.

	// TODO(mdlayher): redesign this to not need a mutex here.
	mu sync.Mutex

	// Per-advertiser metrics.
	LastMulticastTime                       *prometheus.GaugeVec
	MessagesReceivedTotal                   *prometheus.CounterVec
	MessagesReceivedInvalidTotal            *prometheus.CounterVec
	RouterAdvertisementPrefixAutonomous     *prometheus.GaugeVec
	lastRAPA                                metric
	RouterAdvertisementInconsistenciesTotal *prometheus.CounterVec
	RouterAdvertisementsTotal               *prometheus.CounterVec
	ErrorsTotal                             *prometheus.CounterVec
}

// A metric is the labels and value passed into a Prometheus metric when it
// was last updated.
type metric struct {
	Labels []string
	Value  float64
}

// updateGauge updates the input gauge with the specified labels and values,
// and stores the last state of the metric.
func (m *Metrics) updateGauge(g *prometheus.GaugeVec, labels []string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastRAPA = metric{
		Labels: labels,
		Value:  value,
	}

	g.WithLabelValues(labels...).Set(value)
}

// NewMetrics creates and registers Metrics. If reg is nil, the metrics are
// not registered.
func NewMetrics(reg *prometheus.Registry) *Metrics {
	const (
		bld = "build"
		adv = "advertiser"
	)

	mm := &Metrics{
		Info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bld,
			Name:      "info",

			Help: "Metadata about this build of CoreRAD.",
		}, []string{"version"}),

		Time: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bld,
			Name:      "time",

			Help: "The UNIX timestamp of when this build of CoreRAD was produced.",
		}),

		LastMulticastTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: adv,
			Name:      "last_multicast_time_seconds",

			Help: "The UNIX timestamp of when the last multicast router advertisement was sent.",
		}, []string{"interface"}),

		MessagesReceivedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: adv,
			Name:      "messages_received_total",

			Help: "The total number of valid NDP messages received on a listening interface.",
		}, []string{"interface", "message"}),

		MessagesReceivedInvalidTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: adv,
			Name:      "messages_received_invalid_total",

			Help: "The total number of invalid NDP messages received on a listening interface.",
		}, []string{"interface", "message"}),

		RouterAdvertisementPrefixAutonomous: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: adv,
			Name:      "router_advertisement_prefix_autonomous",

			Help: "Indicates whether or not a given prefix assigned to an interface has the autonomous flag set for Stateless Address Autoconfiguration (SLAAC).",
		}, []string{"interface", "prefix"}),

		RouterAdvertisementInconsistenciesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: adv,
			Name:      "router_advertisement_inconsistencies_total",

			Help: "The total number of NDP router advertisements received which contain inconsistent data with this advertiser's configuration.",
		}, []string{"interface"}),

		RouterAdvertisementsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: adv,
			Name:      "router_advertisements_total",

			Help: "The total number of NDP router advertisements sent by the advertiser on an interface.",
		}, []string{"interface", "type"}),

		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: adv,
			Name:      "errors_total",

			Help: "The total number and type of errors that occurred while advertising.",
		}, []string{"interface", "error"}),
	}

	// Initialize any info metrics which are static throughout the lifetime of
	// the program.
	mm.Info.WithLabelValues(build.Version()).Set(1)
	mm.Time.Set(float64(build.Time().Unix()))

	if reg != nil {
		reg.MustRegister(
			mm.Info,
			mm.Time,

			mm.LastMulticastTime,
			mm.MessagesReceivedTotal,
			mm.MessagesReceivedInvalidTotal,
			mm.RouterAdvertisementPrefixAutonomous,
			mm.RouterAdvertisementInconsistenciesTotal,
			mm.RouterAdvertisementsTotal,
		)
	}

	return mm
}

// An interfaceCollector collects Prometheus metrics for a network interface.
type interfaceCollector struct {
	Autoconfiguration *prometheus.Desc
	Forwarding        *prometheus.Desc
	Advertise         *prometheus.Desc

	ifis []config.Interface
}

// newInterfaceCollector creates an interfaceCollector.
func newInterfaceCollector(ifis []config.Interface) prometheus.Collector {
	const subsystem = "interface"

	labels := []string{"interface"}

	return &interfaceCollector{
		Autoconfiguration: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "autoconfiguration"),
			"Indicates whether or not IPv6 autoconfiguration is enabled on this interface.",
			labels,
			nil,
		),

		Forwarding: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "forwarding"),
			"Indicates whether or not IPv6 forwarding is enabled on this interface.",
			labels,
			nil,
		),

		Advertise: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "advertising"),
			"Indicates whether or not NDP router advertisements will be sent from this interface.",
			labels,
			nil,
		),

		ifis: ifis,
	}
}

// Describe implements prometheus.Collector.
func (c *interfaceCollector) Describe(ch chan<- *prometheus.Desc) {
	ds := []*prometheus.Desc{
		c.Autoconfiguration,
		c.Forwarding,
		c.Advertise,
	}

	for _, d := range ds {
		ch <- d
	}

}

// Collect implements prometheus.Collector.
func (c *interfaceCollector) Collect(ch chan<- prometheus.Metric) {
	for _, ifi := range c.ifis {
		auto, err := getIPv6Autoconf(ifi.Name)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(c.Autoconfiguration, err)
			return
		}

		fwd, err := getIPv6Forwarding(ifi.Name)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(c.Forwarding, err)
			return
		}

		ch <- prometheus.MustNewConstMetric(
			c.Autoconfiguration,
			prometheus.GaugeValue,
			boolFloat(auto),
			ifi.Name,
		)

		ch <- prometheus.MustNewConstMetric(
			c.Forwarding,
			prometheus.GaugeValue,
			boolFloat(fwd),
			ifi.Name,
		)

		ch <- prometheus.MustNewConstMetric(
			c.Advertise,
			prometheus.GaugeValue,
			boolFloat(ifi.Advertise),
			ifi.Name,
		)

	}
}

func boolFloat(b bool) float64 {
	if b {
		return 1.0
	}

	return 0.0
}
