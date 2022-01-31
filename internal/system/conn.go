// Copyright 2020-2022 Matt Layher
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
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
)

// A Conn abstracts IPv6 NDP socket operations for purposes of testing.
type Conn interface {
	ReadFrom() (ndp.Message, *ipv6.ControlMessage, net.IP, error)
	SetReadDeadline(t time.Time) error
	WriteTo(m ndp.Message, cm *ipv6.ControlMessage, dst net.IP) error
}

var _ Conn = &ndp.Conn{}

// ErrLinkNotReady is a sentinel which indicates an interface is not ready
// for use with a Dialer listener.
var ErrLinkNotReady = errors.New("link not ready")

// lookupInterface looks up an interface by name, but also returns ErrLinkNotReady
// if the interface doesn't exist.
func lookupInterface(iface string) (*net.Interface, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		if isNoSuchInterface(err) {
			// Allow retry if the interface may not exist yet.
			return nil, fmt.Errorf("interface %q does not exist: %w", iface, ErrLinkNotReady)
		}

		return nil, fmt.Errorf("failed to get interface %q: %v", iface, err)
	}

	return ifi, nil
}

// checkInterface verifies the readiness of an interface.
func checkInterface(ifi *net.Interface, addrFunc func() ([]net.Addr, error)) error {
	// Link must be up.
	if ifi.Flags&net.FlagUp == 0 {
		return fmt.Errorf("interface %q is not up: %w", ifi.Name, ErrLinkNotReady)
	}

	// Link must have an IPv6 link-local unicast address.
	addrs, err := addrFunc()
	if err != nil {
		return fmt.Errorf("failed to get interface %q addresses: %w", ifi.Name, err)
	}

	var foundLL bool
	for _, a := range addrs {
		// Skip non IP and link-local addresses.
		a, ok := a.(*net.IPNet)
		if ok && isIPv6(a.IP) && a.IP.IsLinkLocalUnicast() {
			foundLL = true
			break
		}
	}
	if !foundLL {
		return fmt.Errorf("interface %q has no IPv6 link-local address: %w", ifi.Name, ErrLinkNotReady)
	}

	return nil
}

// isNoSuchInterface determines if an error matches package net's "no such
// interface" error.
func isNoSuchInterface(err error) bool {
	var oerr *net.OpError
	if !errors.As(err, &oerr) {
		return false
	}

	// Unfortunately there's no better way to check this.
	return oerr.Op == "route" &&
		oerr.Net == "ip+net" &&
		oerr.Err.Error() == "no such network interface"
}

// isIPv6 determines if ip is an IPv6 address.
func isIPv6(ip net.IP) bool {
	return ip.To16() != nil && ip.To4() == nil
}
