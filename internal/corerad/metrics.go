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
