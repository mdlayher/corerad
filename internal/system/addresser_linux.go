// Copyright 2021 Matt Layher
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
	"github.com/jsimonetti/rtnetlink"
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
	// Passing this request enables newer kernels to filter the addresses for us
	// to IPv6 only and the specified interface. Older kernels will ignore this
	// and our own filtering code will kick in.
	msgs, err := a.execute(
		&rtnetlink.AddressMessage{
			Family: unix.AF_INET6,
			Index:  uint32(index),
		},
		unix.RTM_GETADDR,
		netlink.Request|netlink.Dump,
	)
	if err != nil {
		return nil, err
	}

	// Unfortunately we can't preallocate as it's possible for older kernels to
	// ignore our filter request and just return all the IP addresses anyway.
	var addrs []IP
	for _, m := range msgs {
		// rtnetlink package invariant checks.
		am, ok := m.(*rtnetlink.AddressMessage)
		if !ok {
			panicf("corerad: invalid rtnetlink message type: %+v", m)
		}
		if am.Family != unix.AF_INET6 || am.Index != uint32(index) {
			// Only want IPv6 addresses for the specified interface.
			continue
		}

		if am.Attributes == nil {
			panicf("corerad: rtnetlink address message missing attributes: %+v", m)
		}

		// Only want IPv6 addresses, and anything with AF_INET6 must be an IPv6
		// address.
		ip, ok := netaddr.FromStdIP(am.Attributes.Address)
		if !ok || !ip.Is6() || ip.Is4in6() {
			panicf("corerad: invalid IPv6 net.IP: %+v", ip)
		}

		// Finally inspect the extended flags in attributes and parse all the
		// address flags for use elsewhere.
		f := am.Attributes.Flags
		addrs = append(addrs, IP{
			Address: netaddr.IPPrefixFrom(ip, am.PrefixLength),

			Deprecated:               f&unix.IFA_F_DEPRECATED != 0,
			ManageTemporaryAddresses: f&unix.IFA_F_MANAGETEMPADDR != 0,
			StablePrivacy:            f&unix.IFA_F_STABLE_PRIVACY != 0,
			Temporary:                f&unix.IFA_F_TEMPORARY != 0,
			Tentative:                f&unix.IFA_F_TENTATIVE != 0,
		})
	}

	return addrs, nil
}

// rtnlExecute executes an rtnetlink request using the operating system's
// netlink sockets.
func rtnlExecute(m rtnetlink.Message, family uint16, flags netlink.HeaderFlags) ([]rtnetlink.Message, error) {
	c, err := rtnetlink.Dial(nil)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	// Socket options are applied on a best-effort basis because they may not be
	// supported on older Linux kernel versions.
	for _, o := range []netlink.ConnOption{
		// Enables better request validation and in-kernel filtering.
		netlink.GetStrictCheck,
		// Enables better error reporting.
		netlink.ExtendedAcknowledge,
	} {
		_ = c.SetOption(o, true)
	}

	return c.Execute(m, family, flags)
}
