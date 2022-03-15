// Copyright 2021-2022 Matt Layher
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

//go:build linux
// +build linux

package system

import (
	"math"
	"net"

	"github.com/jsimonetti/rtnetlink"
	"github.com/mdlayher/ndp"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
	"inet.af/netaddr"
)

var _ Addresser = &addresser{}

// An addresser is an Addresser which uses rtnetlink.
type addresser struct {
	// execute is a swappable function for mocking rtnetlink request/response.
	execute func(m rtnetlink.Message, family uint16, flags netlink.HeaderFlags) ([]rtnetlink.Message, error)
}

// NewAddresser creates an Addresser which executes real rtnetlink requests.
func NewAddresser() Addresser { return &addresser{execute: rtnlExecute} }

// AddressesByIndex implements Addresser.
func (a *addresser) AddressesByIndex(index int) ([]IP, error) {
	// The kernel can filter the addresses for us to return IPv6 addresses only
	// for the given interface.
	msgs, err := a.execute(
		&rtnetlink.AddressMessage{
			Family: unix.AF_INET6,
			Index:  uint32(index),
		},
		unix.RTM_GETADDR,
		netlink.Request|netlink.Dump,
	)
	if err != nil || len(msgs) == 0 {
		return nil, err
	}

	addrs := make([]IP, 0, len(msgs))
	for _, m := range msgs {
		// rtnetlink package invariant checks.
		am, ok := m.(*rtnetlink.AddressMessage)
		if !ok || am.Family != unix.AF_INET6 || am.Attributes == nil {
			panicf("corerad: invalid rtnetlink message type: %+v", m)
		}
		ip, ok := netaddr.FromStdIP(am.Attributes.Address)
		if !ok || !ip.Is6() || ip.Is4in6() {
			panicf("corerad: invalid IPv6 address from rtnetlink: %q", am.Attributes.Address)
		}

		// Note whether the kernel treats this address as valid forever since
		// that means it is static.
		var forever bool
		if am.Attributes.CacheInfo.Valid == math.MaxUint32 {
			forever = true
		}

		f := am.Attributes.Flags
		addrs = append(addrs, IP{
			Address: netaddr.IPPrefixFrom(ip, am.PrefixLength),

			Deprecated:               f&unix.IFA_F_DEPRECATED != 0,
			ManageTemporaryAddresses: f&unix.IFA_F_MANAGETEMPADDR != 0,
			StablePrivacy:            f&unix.IFA_F_STABLE_PRIVACY != 0,
			Temporary:                f&unix.IFA_F_TEMPORARY != 0,
			Tentative:                f&unix.IFA_F_TENTATIVE != 0,

			ValidForever: forever,
		})
	}

	return addrs, nil
}

// AddressesByIndex implements Addresser.
func (a *addresser) LoopbackRoutes() ([]Route, error) {
	// TODO(mdlayher): it appears there is no way to have rtnetlink filter only
	// loopback interfaces on request. For now we filter in userspace.
	ifis, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	// Gather routes for each matching interface.
	//
	// TODO(mdlayher): experiment with multiple loopback interfaces if possible.
	var routes []Route
	for _, ifi := range ifis {
		if ifi.Flags&net.FlagLoopback == 0 || ifi.Flags&net.FlagUp == 0 {
			continue
		}

		rs, err := a.routesByIndex(ifi.Index)
		if err != nil {
			return nil, err
		}

		routes = append(routes, rs...)
	}

	return routes, nil
}

// routesByIndex calls rtnetlink to fetch IPv6 routes for an interface by index.
func (a *addresser) routesByIndex(index int) ([]Route, error) {
	// The kernel can filter the routes for us to return IPv6 routes only for
	// the given out interface.
	msgs, err := a.execute(
		&rtnetlink.RouteMessage{
			Family: unix.AF_INET6,
			Attributes: rtnetlink.RouteAttributes{
				OutIface: uint32(index),
				// Only dump the "main" table for automatic routes. Note that we
				// specify table in the attributes and not the body to enable
				// strict check filtering.
				//
				// TODO(mdlayher): how would we deal with multiple routing
				// tables?
				Table: unix.RT_TABLE_MAIN,
			},
		},
		unix.RTM_GETROUTE,
		netlink.Request|netlink.Dump,
	)
	if err != nil || len(msgs) == 0 {
		return nil, err
	}

	routes := make([]Route, 0, len(msgs))
	for _, m := range msgs {
		// rtnetlink package invariant checks.
		rm, ok := m.(*rtnetlink.RouteMessage)
		if !ok || rm.Family != unix.AF_INET6 {
			panicf("corerad: invalid rtnetlink message type: %+v", m)
		}

		ip, ok := netaddr.FromStdIP(rm.Attributes.Dst)
		if !ok || !ip.Is6() || ip.Is4in6() {
			panicf("corerad: invalid IPv6 route from rtnetlink: %q", rm.Attributes.Dst)
		}

		// Pass along NDP preference if set.
		pref := ndp.Medium
		if p := rm.Attributes.Pref; p != nil {
			pref = ndp.Preference(*p)
		}

		routes = append(routes, Route{
			Prefix:     netaddr.IPPrefixFrom(ip, rm.DstLength),
			Index:      int(rm.Attributes.OutIface),
			Preference: pref,
		})
	}

	return routes, nil
}

// rtnlExecute executes an rtnetlink request using the operating system's
// netlink sockets.
func rtnlExecute(m rtnetlink.Message, family uint16, flags netlink.HeaderFlags) ([]rtnetlink.Message, error) {
	// Unconditionally set strict mode. In practice we don't expect users on
	// older kernels to be running new software like CoreRAD. This simplifies
	// the rest of the rtnetlink calling code by allowing in-kernel filtering of
	// addresses and routes.
	//
	// If this ends up being an issue in practice, the issue can be revisited.
	c, err := rtnetlink.Dial(&netlink.Config{Strict: true})
	if err != nil {
		return nil, err
	}
	defer c.Close()

	return c.Execute(m, family, flags)
}
