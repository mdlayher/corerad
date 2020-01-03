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
	"github.com/mdlayher/corerad/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// AdvertiserMetrics contains metrics for an Advertiser.
type AdvertiserMetrics struct {
	LastMulticastTime            *prometheus.GaugeVec
	MessagesReceivedTotal        *prometheus.CounterVec
	MessagesReceivedInvalidTotal *prometheus.CounterVec
	RouterAdvertisementsTotal    *prometheus.CounterVec
	ErrorsTotal                  *prometheus.CounterVec
	SchedulerWorkers             *prometheus.GaugeVec
}

// NewAdvertiserMetrics creates and registers AdvertiserMetrics. If reg is nil
// the metrics are not registered.
func NewAdvertiserMetrics(reg *prometheus.Registry) *AdvertiserMetrics {
	const subsystem = "advertiser"

	var (
		names = []string{"interface"}
	)

	mm := &AdvertiserMetrics{
		LastMulticastTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "last_multicast_time_seconds",

			Help: "The UNIX timestamp of when the last multicast router advertisement was sent.",
		}, names),

		MessagesReceivedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "messages_received_total",

			Help: "The total number of valid NDP messages received on a listening interface.",
		}, []string{"interface", "message"}),

		MessagesReceivedInvalidTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "messages_received_invalid_total",

			Help: "The total number of invalid NDP messages received on a listening interface.",
		}, []string{"interface", "message"}),

		RouterAdvertisementsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "router_advertisements_total",

			Help: "The total number of NDP router advertisements sent by the advertiser on an interface.",
		}, []string{"interface", "type"}),

		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "errors_total",

			Help: "The total number and type of errors that occurred while advertising.",
		}, []string{"interface", "error"}),

		SchedulerWorkers: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "scheduler_workers",

			Help: "The number of router advertisement scheduler worker goroutines that are running.",
		}, names),
	}

	if reg != nil {
		reg.MustRegister(
			mm.LastMulticastTime,
			mm.MessagesReceivedTotal,
			mm.MessagesReceivedInvalidTotal,
			mm.RouterAdvertisementsTotal,
			mm.SchedulerWorkers,
		)
	}

	return mm
}

// An interfaceCollector collects Prometheus metrics for a network interface.
type interfaceCollector struct {
	Autoconfiguration  *prometheus.Desc
	Forwarding         *prometheus.Desc
	SendAdvertisements *prometheus.Desc

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

		SendAdvertisements: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "send_advertisements"),
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
		c.SendAdvertisements,
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
			c.SendAdvertisements,
			prometheus.GaugeValue,
			boolFloat(ifi.SendAdvertisements),
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
