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

	"github.com/mdlayher/ndp"
)

type raBody struct {
	Interfaces []interfaceBody `json:"interfaces"`
}

type interfaceBody struct {
	Interface     string               `json:"interface"`
	Advertising   bool                 `json:"advertise"`
	Advertisement *routerAdvertisement `json:"advertisement"`
}

type routerAdvertisement struct {
	CurrentHopLimit             int      `json:"current_hop_limit"`
	ManagedConfiguration        bool     `json:"managed_configuration"`
	OtherConfiguration          bool     `json:"other_configuration"`
	MobileIPv6HomeAgent         bool     `json:"mobile_ipv6_home_agent"`
	RouterSelectionPreference   string   `json:"router_selection_preference"`
	NeighborDiscoveryProxy      bool     `json:"neighbor_discovery_proxy"`
	RouterLifetimeSeconds       int      `json:"router_lifetime_seconds"`
	ReachableTimeMilliseconds   int      `json:"reachable_time_milliseconds"`
	RetransmitTimerMilliseconds int      `json:"retransmit_timer_milliseconds"`
	Options                     []option `json:"options"`
}

func packRA(ra *ndp.RouterAdvertisement) *routerAdvertisement {
	return &routerAdvertisement{
		CurrentHopLimit:           int(ra.CurrentHopLimit),
		ManagedConfiguration:      ra.ManagedConfiguration,
		OtherConfiguration:        ra.OtherConfiguration,
		MobileIPv6HomeAgent:       ra.MobileIPv6HomeAgent,
		RouterSelectionPreference: preference(ra.RouterSelectionPreference),
	}
}

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

type option struct {
	// TODO!
}
