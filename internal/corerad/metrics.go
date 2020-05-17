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
	"github.com/mdlayher/corerad/internal/build"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/metricslite"
	"github.com/prometheus/client_golang/prometheus"
)

// Names of metrics which are referenced here and in tests.
const (
	raPrefixAutonomous = "corerad_advertiser_router_advertisement_prefix_autonomous"
	raPrefixOnLink     = "corerad_advertiser_router_advertisement_prefix_on_link"
	raInconsistencies  = "corerad_advertiser_router_advertisement_inconsistencies_total"
)

// Metrics contains metrics for a CoreRAD instance.
type Metrics struct {
	Info metricslite.Gauge
	Time metricslite.Gauge

	// Per-advertiser metrics.
	LastMulticastTime                       metricslite.Gauge
	MessagesReceivedTotal                   metricslite.Counter
	MessagesReceivedInvalidTotal            metricslite.Counter
	RouterAdvertisementPrefixAutonomous     metricslite.Gauge
	RouterAdvertisementPrefixOnLink         metricslite.Gauge
	RouterAdvertisementInconsistenciesTotal metricslite.Counter
	RouterAdvertisementsTotal               metricslite.Counter
	ErrorsTotal                             metricslite.Counter

	// The underlying metrics storage.
	m metricslite.Interface
}

// NewMetrics produces a Metrics structure which will register its metrics to
// the specified metricslite.Interface. If m is nil, metrics are discarded.
func NewMetrics(m metricslite.Interface) *Metrics {
	if m == nil {
		m = metricslite.Discard()
	}

	mm := &Metrics{
		m: m,

		Info: m.Gauge(
			"corerad_build_info",
			"Metadata about this build of CoreRAD.",
			"version",
		),

		Time: m.Gauge(
			"corerad_build_time",
			"The UNIX timestamp of when this build of CoreRAD was produced.",
		),

		LastMulticastTime: m.Gauge(
			"corerad_advertiser_last_multicast_time_seconds",
			"The UNIX timestamp of when the last multicast router advertisement was sent.",
			"interface",
		),

		MessagesReceivedTotal: m.Counter(
			"corerad_advertiser_messages_received_total",
			"The total number of valid NDP messages received on a listening interface.",
			"interface", "message",
		),

		MessagesReceivedInvalidTotal: m.Counter(
			"corerad_advertiser_messages_received_invalid_total",
			"The total number of invalid NDP messages received on a listening interface.",
			"interface", "message",
		),

		RouterAdvertisementPrefixAutonomous: m.Gauge(
			raPrefixAutonomous,
			"Indicates whether or not the Autonomous Address Autoconfiguration (SLAAC) flag is enabled for a given prefix.",
			// TODO: verify uniqueness of prefixes per interface.
			"interface", "prefix",
		),

		RouterAdvertisementPrefixOnLink: m.Gauge(
			raPrefixOnLink,
			"Indicates whether or not the On-Link flag is enabled for a given prefix.",
			// TODO: verify uniqueness of prefixes per interface.
			"interface", "prefix",
		),

		RouterAdvertisementInconsistenciesTotal: m.Counter(
			raInconsistencies,
			"The total number of NDP router advertisements received which contain inconsistent data with this advertiser's configuration.",
			"interface",
		),

		RouterAdvertisementsTotal: m.Counter(
			"corerad_advertiser_router_advertisements_total",
			"The total number of NDP router advertisements sent by the advertiser on an interface.",
			"interface", "type",
		),

		ErrorsTotal: m.Counter(
			"corerad_advertiser_errors_total",
			"The total number and type of errors that occurred while advertising.",
			"interface", "error",
		),
	}

	// Initialize any info metrics which are static throughout the lifetime of
	// the program.
	mm.Info(1, build.Version())
	mm.Time(float64(build.Time().Unix()))

	return mm
}

// Series produces a set of output timeseries from the Metrics, assuming the
// Metrics were initialized with a compatible metricslite.Interface. If not, Series
// will return nil, false.
func (m *Metrics) Series() (map[string]metricslite.Series, bool) {
	type series interface {
		Series() map[string]metricslite.Series
	}

	sm, ok := m.m.(series)
	if !ok {
		// Type does not support Series output.
		return nil, false
	}

	return sm.Series(), true
}

// An interfaceCollector collects Prometheus metrics for a network interface.
type interfaceCollector struct {
	Autoconfiguration *prometheus.Desc
	Forwarding        *prometheus.Desc
	Advertise         *prometheus.Desc

	state system.State
	ifis  []config.Interface
}

// newInterfaceCollector creates an interfaceCollector.
func newInterfaceCollector(state system.State, ifis []config.Interface) prometheus.Collector {
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

		state: state,
		ifis:  ifis,
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
		auto, err := c.state.IPv6Autoconf(ifi.Name)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(c.Autoconfiguration, err)
			return
		}

		fwd, err := c.state.IPv6Forwarding(ifi.Name)
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
