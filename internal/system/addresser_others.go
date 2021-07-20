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

// +build !linux

package system

import (
	"net"

	"inet.af/netaddr"
)

// An addresser is an Addresser which uses package net functions.
type addresser struct{}

// NewAddresser creates an Addresser which uses package net functions.
func NewAddresser() Addresser { return &addresser{} }

// AddressesByIndex implements Addresser.
func (*addresser) AddressesByIndex(index int) ([]IP, error) {
	ifi, err := net.InterfaceByIndex(index)
	if err != nil {
		return nil, err
	}

	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, err
	}

	// Filter out any values which are not IPv6 *net.IPNets.
	var ips []IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}

		ipp, ok := netaddr.FromStdIPNet(ipn)
		if !ok || !ipp.IP().Is6() {
			continue
		}

		// Unfortunately this generic Addresser cannot infer any address flags
		// so just return the IP.
		ips = append(ips, IP{Address: ipp})
	}

	return ips, nil
}
