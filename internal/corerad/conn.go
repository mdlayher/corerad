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

package corerad

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

// TODO: verification of various error cases by swapping out systemConn's functions.

// A conn abstracts IPv6 NDP socket operations for purposes of testing.
type conn interface {
	Close() error
	Dial(iface string) (*net.Interface, net.IP, error)
	IPv6Forwarding() (bool, error)
	ReadFrom() (ndp.Message, *ipv6.ControlMessage, net.IP, error)
	SetReadDeadline(t time.Time) error
	WriteTo(m ndp.Message, cm *ipv6.ControlMessage, dst net.IP) error
}

var _ conn = &systemConn{}

// A systemConn is a conn which manipulates the underlying system's interfaces
// and IPv6 parameters.
type systemConn struct {
	c        *ndp.Conn
	iface    string
	autoPrev bool

	ll *log.Logger
	mm *AdvertiserMetrics

	dial              func(ifi *net.Interface) (*ndp.Conn, net.IP, error)
	checkInterface    func(iface string) (*net.Interface, error)
	getIPv6Forwarding func(iface string) (bool, error)
	getIPv6Autoconf   func(iface string) (bool, error)
	setIPv6Autoconf   func(iface string, enable bool) error
}

// newSystemConn creates a systemConn which manipulates the operating system
// directly.
func newSystemConn(ll *log.Logger, mm *AdvertiserMetrics) *systemConn {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}
	if mm == nil {
		mm = NewAdvertiserMetrics(nil)
	}

	return &systemConn{
		// c, iface, autoPrev initialized on Dial.

		ll: ll,
		mm: mm,

		dial: func(ifi *net.Interface) (*ndp.Conn, net.IP, error) {
			return ndp.Dial(ifi, ndp.LinkLocal)
		},
		checkInterface:    checkInterface,
		getIPv6Forwarding: getIPv6Forwarding,
		getIPv6Autoconf:   getIPv6Autoconf,
		setIPv6Autoconf:   setIPv6Autoconf,
	}
}

// Close implements conn.
func (c *systemConn) Close() error {
	// In general, many of these actions are best-effort and should not halt
	// shutdown on failure.

	if err := c.c.LeaveGroup(net.IPv6linklocalallrouters); err != nil {
		c.logf("failed to leave IPv6 link-local all routers multicast group: %v", err)
	}

	if err := c.c.Close(); err != nil {
		c.logf("failed to stop NDP listener: %v", err)
	}

	// If possible, restore the previous IPv6 autoconfiguration state.
	if err := c.setIPv6Autoconf(c.iface, c.autoPrev); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			c.logf("permission denied while restoring IPv6 autoconfiguration state, continuing anyway (try setting CAP_NET_ADMIN)")
			c.mm.ErrorsTotal.WithLabelValues(c.iface, "configuration").Inc()
		} else {
			return fmt.Errorf("failed to restore IPv6 autoconfiguration on %q: %v", c.iface, err)
		}
	}

	return nil
}

// Dial implements conn.
func (c *systemConn) Dial(iface string) (*net.Interface, net.IP, error) {
	ifi, err := c.checkInterface(iface)
	if err != nil {
		return nil, nil, err
	}

	conn, ip, err := c.dial(ifi)
	if err != nil {
		return nil, nil, err
	}

	// If possible, disable IPv6 autoconfiguration on this interface so that
	// our RAs don't configure more IP addresses on this interface.
	autoPrev, err := c.getIPv6Autoconf(iface)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get IPv6 autoconfiguration state on %q: %v", iface, err)
	}

	if err := c.setIPv6Autoconf(iface, false); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			c.logf("permission denied while disabling IPv6 autoconfiguration, continuing anyway (try setting CAP_NET_ADMIN)")
		} else {
			return nil, nil, fmt.Errorf("failed to disable IPv6 autoconfiguration on %q: %v", iface, err)
		}
	}

	// We only want to accept router solicitation messages.
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)

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

	c.c = conn
	c.iface = iface
	c.autoPrev = autoPrev

	return ifi, ip, nil
}

// IPv6Forwarding implements conn.
func (c *systemConn) IPv6Forwarding() (bool, error) { return c.getIPv6Forwarding(c.iface) }

// ReadFrom implements conn.
func (c *systemConn) ReadFrom() (ndp.Message, *ipv6.ControlMessage, net.IP, error) {
	return c.c.ReadFrom()
}

// SetReadDeadline implements conn.
func (c *systemConn) SetReadDeadline(t time.Time) error { return c.c.SetReadDeadline(t) }

// WriteTo implements conn.
func (c *systemConn) WriteTo(m ndp.Message, cm *ipv6.ControlMessage, dst net.IP) error {
	return c.c.WriteTo(m, cm, dst)
}

// logf prints a formatted log with the systemConn's interface name.
func (c *systemConn) logf(format string, v ...interface{}) {
	c.ll.Println(c.iface + ": " + fmt.Sprintf(format, v...))
}

// errLinkNotReady is a sentinel which indicates an interface is not ready
// for use with an Advertiser.
var errLinkNotReady = errors.New("link not ready")

// checkInterface verifies the readiness of an interface.
func checkInterface(iface string) (*net.Interface, error) {
	// Link must exist.
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		if isNoSuchInterface(err) {
			// Allow retry if the interface may not exist yet.
			return nil, fmt.Errorf("interface %q does not exist: %w", iface, errLinkNotReady)
		}

		return nil, fmt.Errorf("failed to get interface %q: %v", iface, err)
	}

	// Link must have a MAC address (e.g. WireGuard links do not).
	if ifi.HardwareAddr == nil {
		return nil, fmt.Errorf("interface %q has no MAC address", iface)
	}

	// Link must be up.
	// TODO: check point-to-point and multicast flags and configure accordingly.
	if ifi.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("interface %q is not up: %w", iface, errLinkNotReady)
	}

	// Link must have an IPv6 link-local unicast address.
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get interface %q addresses: %w", iface, err)
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
		return nil, fmt.Errorf("interface %q has no IPv6 link-local address: %w", iface, err)
	}

	return ifi, nil
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
