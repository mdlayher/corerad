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
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/metricslite"
	"github.com/mdlayher/ndp"
)

// Names of metrics which are referenced here and in tests.
const (
	ifiAdvertising       = "corerad_interface_advertising"
	ifiAutoconfiguration = "corerad_interface_autoconfiguration"
	ifiForwarding        = "corerad_interface_forwarding"
	ifiMonitoring        = "corerad_interface_monitoring"
	msgInvalid           = "corerad_messages_received_invalid_total"
	advDNSSLLifetime     = "corerad_advertiser_dnssl_lifetime_seconds"
	advInconsistencies   = "corerad_advertiser_inconsistencies_total"
	advPrefixAutonomous  = "corerad_advertiser_prefix_autonomous"
	advPrefixOnLink      = "corerad_advertiser_prefix_on_link"
	advPrefixValid       = "corerad_advertiser_prefix_valid_seconds"
	advPrefixPreferred   = "corerad_advertiser_prefix_preferred_seconds"
	advRDNSSLifetime     = "corerad_advertiser_rdnss_lifetime_seconds"
	monReceived          = "corerad_monitor_messages_received_total"
	monDefaultRoute      = "corerad_monitor_default_route_expiration_timestamp_seconds"
	monPrefixAutonomous  = "corerad_monitor_prefix_autonomous"
	monPrefixOnLink      = "corerad_monitor_prefix_on_link"
	monPrefixPreferred   = "corerad_monitor_prefix_preferred_expiration_timestamp_seconds"
	monPrefixValid       = "corerad_monitor_prefix_valid_expiration_timestamp_seconds"
)

// Metrics contains metrics for a CoreRAD instance.
type Metrics struct {
	// General server metrics.
	Info metricslite.Gauge
	Time metricslite.Gauge

	// Shared per-advertiser/monitor metrics.
	MessagesReceivedInvalidTotal metricslite.Counter

	// Per-advertiser metrics.
	AdvLastMulticastTime                       metricslite.Gauge
	AdvMessagesReceivedTotal                   metricslite.Counter
	AdvRouterAdvertisementInconsistenciesTotal metricslite.Counter
	AdvRouterAdvertisementsTotal               metricslite.Counter
	AdvErrorsTotal                             metricslite.Counter

	// Per-monitor metrics.
	MonMessagesReceivedTotal                 metricslite.Counter
	MonDefaultRouteExpirationTime            metricslite.Gauge
	MonPrefixAutonomous                      metricslite.Gauge
	MonPrefixOnLink                          metricslite.Gauge
	MonPrefixPreferredLifetimeExpirationTime metricslite.Gauge
	MonPrefixValidLifetimeExpirationTime     metricslite.Gauge

	// Used to fetch interface states.
	state system.State
	ifis  []config.Interface

	// The underlying metrics storage.
	m metricslite.Interface
}

// NewMetrics produces a Metrics structure which will register its metrics to
// the specified metricslite.Interface. If m is nil, metrics are discarded.
func NewMetrics(
	m metricslite.Interface,
	version string,
	buildTime time.Time,
	state system.State,
	ifis []config.Interface,
) *Metrics {
	if m == nil {
		m = metricslite.Discard()
	}

	mm := &Metrics{
		m:     m,
		state: state,
		ifis:  ifis,

		Info: m.Gauge(
			"corerad_build_info",
			"Metadata about this build of CoreRAD.",
			"version",
		),

		Time: m.Gauge(
			"corerad_build_timestamp_seconds",
			"The UNIX timestamp of when this build of CoreRAD was produced.",
		),

		MessagesReceivedInvalidTotal: m.Counter(
			msgInvalid,
			"The total number of invalid NDP messages received on an advertising or monitoring interface.",
			"interface", "message",
		),

		AdvLastMulticastTime: m.Gauge(
			"corerad_advertiser_last_multicast_timestamp_seconds",
			"The UNIX timestamp of when the last multicast router advertisement was sent from an advertising interface.",
			"interface",
		),

		AdvMessagesReceivedTotal: m.Counter(
			"corerad_advertiser_messages_received_total",
			"The total number of valid NDP messages received on an advertising interface.",
			"interface", "message",
		),

		AdvRouterAdvertisementInconsistenciesTotal: m.Counter(
			advInconsistencies,
			"The total number of NDP router advertisements received which contain inconsistent data with this advertiser's configuration, partitioned by the problematic field.",
			"interface", "details", "field",
		),

		AdvRouterAdvertisementsTotal: m.Counter(
			"corerad_advertiser_router_advertisements_total",
			"The total number of unicast and/or multicast NDP router advertisements sent by an advertiser on an interface.",
			"interface", "type",
		),

		AdvErrorsTotal: m.Counter(
			"corerad_advertiser_errors_total",
			"The total number and type of errors that occurred while advertising on an interface.",
			"interface", "error",
		),

		MonMessagesReceivedTotal: m.Counter(
			monReceived,
			"The total number of valid NDP messages received on a monitoring interface.",
			"interface", "host", "message",
		),

		MonDefaultRouteExpirationTime: m.Gauge(
			monDefaultRoute,
			"The UNIX timestamp of when the route provided by a default router will expire on a monitoring interface.",
			"interface", "router",
		),

		MonPrefixAutonomous: m.Gauge(
			monPrefixAutonomous,
			"Indicates whether or not the Autonomous Address Autoconfiguration (SLAAC) flag is enabled for a given prefix received on a monitoring interface.",
			"interface", "prefix", "router",
		),

		MonPrefixOnLink: m.Gauge(
			monPrefixOnLink,
			"Indicates whether or not the On-Link flag is enabled for a given prefix received on a monitoring interface.",
			"interface", "prefix", "router",
		),

		MonPrefixPreferredLifetimeExpirationTime: m.Gauge(
			monPrefixPreferred,
			"The UNIX timestamp of when a route to a given prefix should no longer be preferred on a monitoring interface.",
			"interface", "prefix", "router",
		),

		MonPrefixValidLifetimeExpirationTime: m.Gauge(
			monPrefixValid,
			"The UNIX timestamp of when a route to a given prefix will expire on a monitoring interface.",
			"interface", "prefix", "router",
		),
	}

	// Initialize any info metrics which are static throughout the lifetime of
	// the program.
	mm.Info(1, version)

	if buildTime.IsZero() {
		// Report UNIX time 0 if no build time set.
		buildTime = time.Unix(0, 0)
	}
	mm.Time(float64(buildTime.Unix()))

	// Initialize const metrics.
	m.ConstGauge(
		ifiAdvertising,
		"Indicates whether or not NDP router advertisements will be sent from this interface.",
		"interface",
	)

	m.ConstGauge(
		ifiMonitoring,
		"Indicates whether or not NDP messages will be monitored on this interface.",
		"interface",
	)

	m.ConstGauge(
		ifiAutoconfiguration,
		"Indicates whether or not IPv6 autoconfiguration is enabled on this interface.",
		"interface",
	)

	m.ConstGauge(
		ifiForwarding,
		"Indicates whether or not IPv6 forwarding is enabled on this interface.",
		"interface",
	)

	m.ConstGauge(
		advDNSSLLifetime,
		"The amount of time in seconds that clients should consider advertised DNS search list domain names valid.",
		"interface", "domains",
	)

	m.ConstGauge(
		advPrefixAutonomous,
		"Indicates whether or not the Autonomous Address Autoconfiguration (SLAAC) flag is enabled for a given advertised prefix.",
		"interface", "prefix",
	)

	m.ConstGauge(
		advPrefixOnLink,
		"Indicates whether or not the On-Link flag is enabled for a given advertised prefix.",
		"interface", "prefix",
	)

	m.ConstGauge(
		advPrefixValid,
		"The amount of time in seconds that clients should consider this advertised prefix valid for on-link determination.",
		"interface", "prefix",
	)

	m.ConstGauge(
		advPrefixPreferred,
		"The amount of time in seconds that addresses generated via SLAAC by clients should remain preferred.",
		"interface", "prefix",
	)

	m.ConstGauge(
		advRDNSSLifetime,
		"The amount of time in seconds that clients should consider advertised recursive DNS servers valid.",
		"interface", "servers",
	)

	// Enable const metrics collection.
	m.OnConstScrape(mm.constScrape)

	return mm
}

// constScrape is a metricslite.ScrapeFunc which gathers const metrics related
// to current interface and RA state.
func (m *Metrics) constScrape(metrics map[string]func(float64, ...string)) error {
	// Report errors for a fixed const metric throughout, since we generate
	// all information for metrics reporting before collecting metrics.
	errorf := func(format string, v ...interface{}) *metricslite.ScrapeError {
		return &metricslite.ScrapeError{
			Metric: ifiForwarding,
			Err:    fmt.Errorf(format, v...),
		}
	}

	for _, ifi := range m.ifis {
		auto, err := m.state.IPv6Autoconf(ifi.Name)
		if err != nil {
			return errorf("failed to check IPv6 autoconfiguration for %q: %v", ifi.Name, err)
		}

		fwd, err := m.state.IPv6Forwarding(ifi.Name)
		if err != nil {
			return errorf("failed to check IPv6 forwarding for %q: %v", ifi.Name, err)
		}

		var ra *ndp.RouterAdvertisement
		if ifi.Advertise {
			// Generate a current RA advertising interfaces and report on it.
			//
			// TODO(mdlayher): plumb in misconfigurations.
			ra, _, err = ifi.RouterAdvertisement(fwd)
			if err != nil {
				return errorf("failed to generate router advertisement for metrics for %q: %v", ifi.Name, err)
			}
		}

		collectMetrics(metrics, metricsContext{
			Interface:         ifi.Name,
			Advertising:       ifi.Advertise,
			Autoconfiguration: auto,
			Forwarding:        fwd,
			Monitoring:        ifi.Monitor,
			Advertisement:     ra,
		})
	}

	return nil
}

// A metricsContext contains arguments used to populate metrics in collectMetrics.
type metricsContext struct {
	Interface                                              string
	Advertising, Autoconfiguration, Forwarding, Monitoring bool
	Advertisement                                          *ndp.RouterAdvertisement
}

// collectMetrics sets const metrics using the input data for the specified
// interface.
func collectMetrics(metrics map[string]func(float64, ...string), mctx metricsContext) {
	var (
		dnssl    []*ndp.DNSSearchList
		prefixes []*ndp.PrefixInformation
		rdnss    []*ndp.RecursiveDNSServer
	)

	if mctx.Advertisement != nil {
		// Gather options for metrics reporting since a non-nil advertisement
		// was passed.
		dnssl = pick[*ndp.DNSSearchList](mctx.Advertisement.Options)
		prefixes = pick[*ndp.PrefixInformation](mctx.Advertisement.Options)
		rdnss = pick[*ndp.RecursiveDNSServer](mctx.Advertisement.Options)
	}

	for m, c := range metrics {
		switch m {
		case ifiAdvertising:
			c(boolFloat(mctx.Advertising), mctx.Interface)
		case ifiAutoconfiguration:
			c(boolFloat(mctx.Autoconfiguration), mctx.Interface)
		case ifiForwarding:
			c(boolFloat(mctx.Forwarding), mctx.Interface)
		case ifiMonitoring:
			c(boolFloat(mctx.Monitoring), mctx.Interface)
		case advDNSSLLifetime:
			for _, d := range dnssl {
				c(d.Lifetime.Seconds(), mctx.Interface, strings.Join(d.DomainNames, ", "))
			}
		case advPrefixAutonomous, advPrefixOnLink, advPrefixValid, advPrefixPreferred:
			for _, p := range prefixes {
				switch m {
				case advPrefixAutonomous:
					c(boolFloat(p.AutonomousAddressConfiguration), mctx.Interface, prefixStr(p))
				case advPrefixOnLink:
					c(boolFloat(p.OnLink), mctx.Interface, prefixStr(p))
				case advPrefixValid:
					c(p.ValidLifetime.Seconds(), mctx.Interface, prefixStr(p))
				case advPrefixPreferred:
					c(p.PreferredLifetime.Seconds(), mctx.Interface, prefixStr(p))
				default:
					panicf("corerad: prefix metrics collection for %q is not handled", m)
				}
			}
		case advRDNSSLifetime:
			for _, r := range rdnss {
				c(r.Lifetime.Seconds(), mctx.Interface, stringerStr(r.Servers))
			}
		default:
			panicf("corerad: metrics collection for %q is not handled", m)
		}
	}
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

func prefixStr(p *ndp.PrefixInformation) string { return cidrStr(p.Prefix, p.PrefixLength) }
func routeStr(r *ndp.RouteInformation) string   { return cidrStr(r.Prefix, r.PrefixLength) }

func cidrStr(prefix netip.Addr, length uint8) string {
	return netip.PrefixFrom(prefix, int(length)).String()
}

func stringerStr[T fmt.Stringer](strs []T) string {
	ss := make([]string, 0, len(strs))
	for _, s := range strs {
		ss = append(ss, s.String())
	}

	return strings.Join(ss, ", ")
}

func boolFloat(b bool) float64 {
	if b {
		return 1.0
	}

	return 0.0
}
