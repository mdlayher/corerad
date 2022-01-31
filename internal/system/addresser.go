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

package system

import (
	"net"

	"inet.af/netaddr"
)

// An Addresser is a type that can fetch IP address information from the
// operating system.
type Addresser interface {
	AddressesByIndex(index int) ([]IP, error)
}

// An IP is an IP address and its associated operating system-specific metadata.
type IP struct {
	// The IP address of an interface. Note that address is not actually a
	// "prefix" but instead an IP address and its associated CIDR mask, which
	// may be in non-canonical form such as 2001:db8::1/64.
	Address netaddr.IPPrefix

	// Interface flags fetched from the operating system which are used for
	// address preference logic.
	Deprecated, ManageTemporaryAddresses, StablePrivacy, Temporary, Tentative bool

	// Reports whether the operating system treats this address as valid
	// forever, as is the case for static addresses.
	ValidForever bool
}

// A netAddresser is a generic Addresser which uses package net functions.
type netAddresser struct{}

// NewNetAddresser creates an Addresser which uses package net functions.
func NewNetAddresser() Addresser { return &netAddresser{} }

// AddressesByIndex implements Addresser.
func (*netAddresser) AddressesByIndex(index int) ([]IP, error) {
	ifi, err := net.InterfaceByIndex(index)
	if err != nil {
		return nil, err
	}

	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, err
	}

	// Filter out any values which are not IPv6 *net.IPNets. Don't preallocate
	// ips because filtering is required.
	var ips []IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}

		ipp, ok := netaddr.FromStdIPNet(ipn)
		if !ok || !ipp.IP().Is6() || ipp.IP().Is4in6() {
			continue
		}

		// Unfortunately this generic Addresser cannot infer any address flags
		// so just return the IP.
		ips = append(ips, IP{Address: ipp})
	}

	return ips, nil
}
