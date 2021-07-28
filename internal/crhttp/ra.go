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

package crhttp

import (
	"fmt"
	"net"

	"github.com/mdlayher/ndp"
)

// An interfacesBody is the top-level structure returned by the debug API's
// interfaces route.
type interfacesBody struct {
	Interfaces []interfaceBody `json:"interfaces"`
}

// An interfaceBody represents an individual advertising interface.
type interfaceBody struct {
	Interface   string `json:"interface"`
	Advertising bool   `json:"advertise"`

	// Nil if Advertising is false.
	Advertisement *routerAdvertisement `json:"advertisement"`
}

// A routerAdvertisement represents an unpacked NDP router advertisement.
type routerAdvertisement struct {
	CurrentHopLimit             int     `json:"current_hop_limit"`
	ManagedConfiguration        bool    `json:"managed_configuration"`
	OtherConfiguration          bool    `json:"other_configuration"`
	MobileIPv6HomeAgent         bool    `json:"mobile_ipv6_home_agent"`
	RouterSelectionPreference   string  `json:"router_selection_preference"`
	NeighborDiscoveryProxy      bool    `json:"neighbor_discovery_proxy"`
	RouterLifetimeSeconds       int     `json:"router_lifetime_seconds"`
	ReachableTimeMilliseconds   int     `json:"reachable_time_milliseconds"`
	RetransmitTimerMilliseconds int     `json:"retransmit_timer_milliseconds"`
	Options                     options `json:"options"`
}

// packRA packs the data from an RA into a routerAdvertisement structure.
func packRA(ra *ndp.RouterAdvertisement) *routerAdvertisement {
	return &routerAdvertisement{
		CurrentHopLimit:             int(ra.CurrentHopLimit),
		ManagedConfiguration:        ra.ManagedConfiguration,
		OtherConfiguration:          ra.OtherConfiguration,
		MobileIPv6HomeAgent:         ra.MobileIPv6HomeAgent,
		RouterSelectionPreference:   preference(ra.RouterSelectionPreference),
		NeighborDiscoveryProxy:      ra.NeighborDiscoveryProxy,
		RouterLifetimeSeconds:       int(ra.RouterLifetime.Seconds()),
		ReachableTimeMilliseconds:   int(ra.ReachableTime.Milliseconds()),
		RetransmitTimerMilliseconds: int(ra.RetransmitTimer.Milliseconds()),
		Options:                     packOptions(ra.Options),
	}
}

// preference returns a stringified preference value for p.
func preference(p ndp.Preference) string {
	switch p {
	case ndp.Low:
		return "low"
	case ndp.Medium:
		return "medium"
	case ndp.High:
		return "high"
	default:
		panic(fmt.Sprintf("crhttp: invalid ndp.Preference %q", p.String()))
	}
}

// options represents the options unpacked from an NDP router advertisement.
type options struct {
	DNSSL                  []dnssl  `json:"dnssl"`
	MTU                    int      `json:"mtu"`
	Prefixes               []prefix `json:"prefixes"`
	RDNSS                  []rdnss  `json:"rdnss"`
	Routes                 []route  `json:"routes"`
	SourceLinkLayerAddress string   `json:"source_link_layer_address"`
	CaptivePortal          string   `json:"captive_portal"`
}

// A dnssl represents an NDP DNS Search List option.
type dnssl struct {
	LifetimeSeconds int      `json:"lifetime_seconds"`
	DomainNames     []string `json:"domain_names"`
}

// A prefix represents an NDP Prefix Information option.
type prefix struct {
	Prefix                             string `json:"prefix"`
	OnLink                             bool   `json:"on_link"`
	AutonomousAddressAutoconfiguration bool   `json:"autonomous_address_autoconfiguration"`
	ValidLifetimeSeconds               int    `json:"valid_lifetime_seconds"`
	PreferredLifetimeSeconds           int    `json:"preferred_lifetime_seconds"`
}

// A RDNSS represents an NDP Recursive DNS Servers option.
type rdnss struct {
	LifetimeSeconds int      `json:"lifetime_seconds"`
	Servers         []string `json:"servers"`
}

// A route represents an NDP Prefix Information option.
type route struct {
	Prefix               string `json:"prefix"`
	Preference           string `json:"preference"`
	RouteLifetimeSeconds int    `json:"route_lifetime_seconds"`
}

// packOptions unpacks individual NDP options to produce an options structure.
func packOptions(opts []ndp.Option) options {
	var out options
	for _, o := range opts {
		switch o := o.(type) {
		case *ndp.CaptivePortal:
			out.CaptivePortal = string(*o)
		case *ndp.DNSSearchList:
			out.DNSSL = append(out.DNSSL, dnssl{
				LifetimeSeconds: int(o.Lifetime.Seconds()),
				DomainNames:     o.DomainNames,
			})
		case *ndp.LinkLayerAddress:
			out.SourceLinkLayerAddress = o.Addr.String()
		case *ndp.MTU:
			out.MTU = int(*o)
		case *ndp.PrefixInformation:
			out.Prefixes = append(out.Prefixes, prefix{
				Prefix:                             prefixString(o.Prefix, o.PrefixLength),
				OnLink:                             o.OnLink,
				AutonomousAddressAutoconfiguration: o.AutonomousAddressConfiguration,
				ValidLifetimeSeconds:               int(o.ValidLifetime.Seconds()),
				PreferredLifetimeSeconds:           int(o.PreferredLifetime.Seconds()),
			})
		case *ndp.RecursiveDNSServer:
			servers := make([]string, 0, len(o.Servers))
			for _, s := range o.Servers {
				servers = append(servers, s.String())
			}

			out.RDNSS = append(out.RDNSS, rdnss{
				LifetimeSeconds: int(o.Lifetime.Seconds()),
				Servers:         servers,
			})
		case *ndp.RouteInformation:
			out.Routes = append(out.Routes, route{
				// Pack prefix and mask into a combined CIDR notation string.
				Prefix:               prefixString(o.Prefix, o.PrefixLength),
				Preference:           preference(o.Preference),
				RouteLifetimeSeconds: int(o.RouteLifetime.Seconds()),
			})
		default:
			panicf("crhttp: unhandled NDP option: %#v", o)
		}
	}

	return out
}

// prefixString combines prefix and length into a CIDR notation string.
func prefixString(prefix net.IP, length uint8) string {
	return (&net.IPNet{
		IP:   prefix,
		Mask: net.CIDRMask(int(length), 128),
	}).String()
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
