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

package system

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"time"

	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
)

// TODO: verification of various error cases by swapping out NDPConn's functions.

// A Conn abstracts IPv6 NDP socket operations for purposes of testing.
type Conn interface {
	Close() error
	Dial(iface string) (*net.Interface, net.IP, error)
	ReadFrom() (ndp.Message, *ipv6.ControlMessage, net.IP, error)
	SetReadDeadline(t time.Time) error
	WriteTo(m ndp.Message, cm *ipv6.ControlMessage, dst net.IP) error
}

// An NDPConn is a Conn which communicates over ICMPv6 using NDP.
type NDPConn struct {
	Conn     *ndp.Conn
	iface    string
	autoPrev bool

	ll *log.Logger
}

var _ Conn = &NDPConn{}

// NewConn creates an NDPConn which uses ICMPv6 sockets.
func NewConn(ll *log.Logger) *NDPConn {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	return &NDPConn{
		// Conn, iface, autoPrev initialized on Dial.
		ll: ll,
	}
}

// Close implements Conn.
func (c *NDPConn) Close() error {
	// In general, many of these actions are best-effort and should not halt
	// shutdown on failure.

	if err := c.Conn.LeaveGroup(net.IPv6linklocalallrouters); err != nil {
		c.logf("failed to leave IPv6 link-local all routers multicast group: %v", err)
	}

	if err := c.Conn.Close(); err != nil {
		c.logf("failed to stop NDP listener: %v", err)
	}

	// If possible, restore the previous IPv6 autoconfiguration state.
	if err := setIPv6Autoconf(c.iface, c.autoPrev); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			c.logf("permission denied while restoring IPv6 autoconfiguration state, continuing anyway (try setting CAP_NET_ADMIN)")
		} else {
			return fmt.Errorf("failed to restore IPv6 autoconfiguration on %q: %v", c.iface, err)
		}
	}

	return nil
}

// Dial implements Conn.
func (c *NDPConn) Dial(iface string) (*net.Interface, net.IP, error) {
	ifi, err := lookupInterface(iface)
	if err != nil {
		return nil, nil, err
	}

	if err := checkInterface(ifi, ifi.Addrs); err != nil {
		return nil, nil, err
	}

	conn, ip, err := ndp.Dial(ifi, ndp.LinkLocal)
	if err != nil {
		return nil, nil, err
	}

	// If possible, disable IPv6 autoconfiguration on this interface so that
	// our RAs don't configure more IP addresses on this interface.
	autoPrev, err := getIPv6Autoconf(iface)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get IPv6 autoconfiguration state on %q: %v", iface, err)
	}

	if err := setIPv6Autoconf(iface, false); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			c.logf("permission denied while disabling IPv6 autoconfiguration, continuing anyway (try setting CAP_NET_ADMIN)")
		} else {
			return nil, nil, fmt.Errorf("failed to disable IPv6 autoconfiguration on %q: %v", iface, err)
		}
	}

	// Accept router solicitations to generate advertisements and other routers'
	// advertisements to verify them against our own.
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)
	f.Accept(ipv6.ICMPTypeRouterAdvertisement)

	if err := conn.SetICMPFilter(&f); err != nil {
		return nil, nil, fmt.Errorf("failed to apply ICMPv6 filter: %v", err)
	}

	// Enable inspection of IPv6 control messages.
	flags := ipv6.FlagHopLimit
	if err := conn.SetControlMessage(flags, true); err != nil {
		return nil, nil, fmt.Errorf("failed to apply IPv6 control message flags: %v", err)
	}

	// We are now a router.
	if err := conn.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return nil, nil, fmt.Errorf("failed to join IPv6 link-local all routers multicast group: %v", err)
	}

	c.Conn = conn
	c.iface = iface
	c.autoPrev = autoPrev

	return ifi, ip, nil
}

// ReadFrom implements Conn.
func (c *NDPConn) ReadFrom() (ndp.Message, *ipv6.ControlMessage, net.IP, error) {
	return c.Conn.ReadFrom()
}

// SetReadDeadline implements Conn.
func (c *NDPConn) SetReadDeadline(t time.Time) error { return c.Conn.SetReadDeadline(t) }

// WriteTo implements Conn.
func (c *NDPConn) WriteTo(m ndp.Message, cm *ipv6.ControlMessage, dst net.IP) error {
	return c.Conn.WriteTo(m, cm, dst)
}

// logf prints a formatted log with the NDPConn's interface name.
func (c *NDPConn) logf(format string, v ...interface{}) {
	c.ll.Println(c.iface + ": " + fmt.Sprintf(format, v...))
}

// ErrLinkNotReady is a sentinel which indicates an interface is not ready
// for use with an Advertiser.
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
	// Link must have a MAC address (e.g. WireGuard links do not).
	if ifi.HardwareAddr == nil {
		return fmt.Errorf("interface %q has no MAC address", ifi.Name)
	}

	// Link must be up.
	// TODO: check point-to-point and multicast flags and configure accordingly.
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
