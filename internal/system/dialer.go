// Copyright 2019 Matt Layher
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
	"context"
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

// A Dialer can create Conns which can be reinitialized on certain errors.
type Dialer struct {
	// DialFunc specifies a function which will override the default dialing
	// logic to produce an arbitrary DialContext. DialFunc should only be
	// set in tests.
	DialFunc func() *DialContext

	iface string
	ll    *log.Logger
}

// NewDialer creates a Dialer using the specified logger and network interface.
func NewDialer(ll *log.Logger, iface string) *Dialer {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	return &Dialer{
		iface: iface,
		ll:    ll,
	}
}

// A DialContext stores data used in the context of a Dialer.Dial closure.
type DialContext struct {
	Conn      Conn
	Interface *net.Interface
	IP        net.IP

	done func() error
}

// TODO(mdlayher): tests for reinit logic on Dialer type now that it has been
// factored out from the Advertiser.

// TODO(mdlayher): reincorporate netstate.Watcher.

// Dial creates a Conn and invokes fn with a populated DialContext for the
// caller's use. If fn returns an error which is considered retryable, Dial
// will attempt to re-dial the Conn and invoke fn again.
func (d *Dialer) Dial(ctx context.Context, fn func(ctx context.Context, dctx *DialContext) error) error {
	if d.DialFunc != nil {
		return fn(ctx, d.DialFunc())
	}

	// err is reused for each loop, and intentionally nil on the first pass
	// so initialization occurs.
	var err error
	for {
		// Either initialize or reinitialize the DialContext based on the value
		// of err, and whether or not err is recoverable.
		dctx, err := d.init(ctx, err)
		if err != nil {
			// Don't block user shutdown.
			if errors.Is(err, context.Canceled) {
				return nil
			}

			return fmt.Errorf("failed to reinitialize %q advertiser: %v", d.iface, err)
		}

		// Invoke the user's function and track any errors returned. Once the
		// function returns, the connection we passed in via dctx should be
		// closed so it can either be cleaned up or reinitialized on the next
		// loop iteration.
		err = fn(ctx, dctx)
		if derr := dctx.done(); derr != nil {
			return fmt.Errorf("failed to clean up connection: %v", derr)
		}
		if err == nil {
			// No error, all done.
			return nil
		}
	}
}

// errLinkChange is a sentinel value which indicates a link state change.
var errLinkChange = errors.New("link state change")

// reinit attempts repeated reinitialization of the Advertiser based on whether
// the input error is considered recoverbale.
func (d *Dialer) init(ctx context.Context, err error) (*DialContext, error) {
	// Verify the interface is available and ready for listening.
	var dctx *DialContext
	if err == nil {
		// Nil input error, this must be the first initialization.
		dctx, err = d.dial()
	}

	// Check for conditions which are recoverable.
	var serr *os.SyscallError
	switch {
	case errors.As(err, &serr):
		if errors.Is(serr, os.ErrPermission) {
			// Permission denied means this will never work, so exit immediately.
			return nil, err
		}

		// For other syscall errors, try again.
		d.logf("error advertising, reinitializing")
	case errors.Is(err, errLinkNotReady):
		d.logf("interface not ready, reinitializing")
	case errors.Is(err, errLinkChange):
		d.logf("interface state changed, reinitializing")
	case err == nil:
		// Successful init.
		return dctx, nil
	default:
		// Unrecoverable error
		return nil, err
	}

	// Recoverable error, try to initialize for delay*attempts seconds, every
	// delay seconds.
	const (
		attempts = 40
		delay    = 3 * time.Second
	)

	for i := 0; i < attempts; i++ {
		// Don't wait on the first attempt.
		if i != 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		dctx, err := d.dial()
		if err != nil {
			d.logf("retrying initialization, %d attempt(s) remaining: %v", attempts-(i+1), err)
			continue
		}

		return dctx, nil
	}

	return nil, fmt.Errorf("timed out trying to initialize after error: %v", err)
}

// dial produces a DialContext after preparing an interface to handle IPv6
// NDP traffic.
func (d *Dialer) dial() (*DialContext, error) {
	ifi, err := lookupInterface(d.iface)
	if err != nil {
		return nil, err
	}

	if err := checkInterface(ifi, ifi.Addrs); err != nil {
		return nil, err
	}

	conn, ip, err := dialNDP(ifi)
	if err != nil {
		return nil, err
	}

	// If possible, disable IPv6 autoconfiguration on this interface so that
	// our RAs don't configure more IP addresses on this interface.
	autoPrev, err := getIPv6Autoconf(d.iface)
	if err != nil {
		return nil, fmt.Errorf("failed to get IPv6 autoconfiguration state on %q: %v", d.iface, err)
	}

	if err := setIPv6Autoconf(d.iface, false); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			d.logf("permission denied while disabling IPv6 autoconfiguration, continuing anyway (try setting CAP_NET_ADMIN)")
		} else {
			return nil, fmt.Errorf("failed to disable IPv6 autoconfiguration on %q: %v", d.iface, err)
		}
	}

	// Return a function which can be used to undo all of the previous operations
	// when the DialContext is no longer needed.
	done := func() error {
		// In general, many of these actions are best-effort and should not halt
		// shutdown on failure.

		if err := conn.LeaveGroup(net.IPv6linklocalallrouters); err != nil {
			d.logf("failed to leave IPv6 link-local all routers multicast group: %v", err)
		}

		if err := conn.Close(); err != nil {
			d.logf("failed to stop NDP listener: %v", err)
		}

		// If possible, restore the previous IPv6 autoconfiguration state.
		if err := setIPv6Autoconf(d.iface, autoPrev); err != nil {
			if errors.Is(err, os.ErrPermission) {
				// Continue anyway but provide a hint.
				d.logf("permission denied while restoring IPv6 autoconfiguration state, continuing anyway (try setting CAP_NET_ADMIN)")
			} else {
				return fmt.Errorf("failed to restore IPv6 autoconfiguration on %q: %v", d.iface, err)
			}
		}

		return nil
	}

	return &DialContext{
		Conn:      conn,
		Interface: ifi,
		IP:        ip,
		done:      done,
	}, nil
}

// logf prints a formatted log with the Dialer's interface name.
func (d *Dialer) logf(format string, v ...interface{}) {
	d.ll.Println(d.iface + ": " + fmt.Sprintf(format, v...))
}

// dialNDP creates an ndp.Conn which is ready to serve router advertisements.
func dialNDP(ifi *net.Interface) (*ndp.Conn, net.IP, error) {
	c, ip, err := ndp.Dial(ifi, ndp.LinkLocal)
	if err != nil {
		return nil, nil, err
	}

	// Accept router solicitations to generate advertisements and other routers'
	// advertisements to verify them against our own.
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)
	f.Accept(ipv6.ICMPTypeRouterAdvertisement)

	if err := c.SetICMPFilter(&f); err != nil {
		return nil, nil, fmt.Errorf("failed to apply ICMPv6 filter: %v", err)
	}

	// Enable inspection of IPv6 control messages.
	if err := c.SetControlMessage(ipv6.FlagHopLimit, true); err != nil {
		return nil, nil, fmt.Errorf("failed to apply IPv6 control message flags: %v", err)
	}

	// We are now a router.
	if err := c.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return nil, nil, fmt.Errorf("failed to join IPv6 link-local all routers multicast group: %v", err)
	}

	return c, ip, nil
}
