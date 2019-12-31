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
	"strconv"

	"github.com/mdlayher/corerad/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// AdvertiserMetrics contains metrics for an Advertiser.
type AdvertiserMetrics struct {
	LastMulticastTime         *prometheus.GaugeVec
	MessagesReceivedTotal     *prometheus.CounterVec
	RouterAdvertisementsTotal *prometheus.CounterVec
	ErrorsTotal               *prometheus.CounterVec
	SchedulerWorkers          *prometheus.GaugeVec
	SchedulerWorkersBusy      *prometheus.GaugeVec
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

			Help: "The total number of NDP messages received on a listening interface.",
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

			Help: "The number of configured router advertisement scheduler worker goroutines.",
		}, names),

		SchedulerWorkersBusy: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "scheduler_workers_busy",

			Help: "The number of router advertisement scheduler worker goroutines that are busy working.",
		}, names),
	}

	if reg != nil {
		reg.MustRegister(
			mm.LastMulticastTime,
			mm.MessagesReceivedTotal,
			mm.RouterAdvertisementsTotal,
			mm.SchedulerWorkers,
			mm.SchedulerWorkersBusy,
		)
	}

	return mm
}

// An interfaceCollector collects Prometheus metrics for a network interface.
type interfaceCollector struct {
	Info *prometheus.Desc

	ifis []config.Interface
}

// newInterfaceCollector creates an interfaceCollector.
func newInterfaceCollector(ifis []config.Interface) prometheus.Collector {
	return &interfaceCollector{
		Info: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "interface", "info"),
			"Metadata about an interface's CoreRAD and system configuration.",
			[]string{"interface", "send_advertisements", "autoconfiguration", "forwarding"},
			nil,
		),

		ifis: ifis,
	}
}

// Describe implements prometheus.Collector.
func (c *interfaceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.Info
}

// Collect implements prometheus.Collector.
func (c *interfaceCollector) Collect(ch chan<- prometheus.Metric) {
	for _, ifi := range c.ifis {
		auto, err := getIPv6Autoconf(ifi.Name)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(c.Info, err)
			return
		}

		fwd, err := getIPv6Forwarding(ifi.Name)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(c.Info, err)
			return
		}

		ch <- prometheus.MustNewConstMetric(
			c.Info,
			prometheus.GaugeValue,
			1,
			ifi.Name,
			strconv.FormatBool(ifi.SendAdvertisements),
			strconv.FormatBool(auto),
			strconv.FormatBool(fwd),
		)
	}
}
