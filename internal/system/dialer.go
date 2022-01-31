// Copyright 2019-2022 Matt Layher
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
	DialFunc func() (*DialContext, error)

	iface string
	state State
	mode  DialerMode
	ll    *log.Logger
}

// A DialerMode specifies a mode of operation for the Dialer.
type DialerMode int

// Possible DialerMode values.
const (
	_ DialerMode = iota
	Advertise
	Monitor
)

// NewDialer creates a Dialer using the specified logger and network interface.
func NewDialer(iface string, state State, mode DialerMode, ll *log.Logger) *Dialer {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	switch mode {
	case Advertise, Monitor:
	default:
		panicf("system: invalid DialerMode: %d", mode)
	}

	d := &Dialer{
		iface: iface,
		state: state,
		mode:  mode,
		ll:    ll,
	}
	d.DialFunc = d.dial

	return d
}

// A DialContext stores data used in the context of a Dialer.Dial closure.
type DialContext struct {
	Conn      Conn
	Interface *net.Interface
	IP        net.IP

	done func() error
}

// Dial creates a Conn and invokes fn with a populated DialContext for the
// caller's use. If fn returns an error which is considered retryable, Dial
// will attempt to re-dial the Conn and invoke fn again.
func (d *Dialer) Dial(ctx context.Context, fn func(ctx context.Context, dctx *DialContext) error) error {
	// Variables are reused for each loop, and error is intentionally nil on the
	// first pass so initialization occurs.
	var (
		dctx *DialContext
		err  error
	)

	for {
		// Either initialize or reinitialize the DialContext based on the value
		// of err, and whether or not err is recoverable.
		dctx, err = d.init(ctx, err)
		if err != nil {
			// Don't block user shutdown.
			if errors.Is(err, context.Canceled) {
				return nil
			}

			return fmt.Errorf("failed to reinitialize %q listener: %w", d.iface, err)
		}

		// Invoke the user's function and track any errors returned. Once the
		// function returns, the connection we passed in via dctx should be
		// closed so it can either be cleaned up or reinitialized on the next
		// loop iteration.
		err = fn(ctx, dctx)
		if dctx.done != nil {
			if derr := dctx.done(); derr != nil {
				return fmt.Errorf("failed to clean up connection: %v", derr)
			}
		}
		if err == nil {
			// No error, all done.
			return nil
		}
	}
}

// ErrLinkChange is a sentinel value which indicates a link state change.
var ErrLinkChange = errors.New("link state change")

// reinit attempts repeated reinitialization of the Advertiser based on whether
// the input error is considered recoverbale.
func (d *Dialer) init(ctx context.Context, err error) (*DialContext, error) {
	// Verify the interface is available and ready for listening.
	var dctx *DialContext
	if err == nil {
		// Nil input error, this must be the first initialization.
		dctx, err = d.DialFunc()
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
		d.logf("error listening, reinitializing: %v", err)
	case errors.Is(err, ErrLinkNotReady):
		d.logf("interface not ready, reinitializing")
	case errors.Is(err, ErrLinkChange):
		d.logf("interface state changed, reinitializing")
	case err == nil:
		// Successful init.
		return dctx, nil
	default:
		// Unrecoverable error
		return nil, err
	}

	// Recoverable error, try to initialize with backoff and retry.
	const (
		attempts = 50
		maxDelay = 3 * time.Second
	)

	var delay time.Duration
	for i := 0; i < attempts; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
			// Add 250ms every iteration, up to maxDelay time.
			delay = time.Duration(i+1) * 250 * time.Millisecond
			if delay > maxDelay {
				delay = maxDelay
			}
		}

		dctx, err := d.DialFunc()
		if err != nil {
			d.logf("retrying initialization in %s, %d attempt(s) remaining: %v", delay, attempts-(i+1), err)
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

	// For Advertise mode only, we must temporarily disable IPv6 autoconfiguration.
	// When the Dialer closes a Conn, restore will be invoked to restore the
	// previous state of the interface.
	var restore func() error
	if d.mode == Advertise {
		restore, err = d.setAutoconf()
		if err != nil {
			return nil, err
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

		if restore != nil {
			return restore()
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

// setAutoconf disable IPv6 autoconfiguration for the Dialer's interface and
// returns a function which restores the previous configuration when invoked.
func (d *Dialer) setAutoconf() (func() error, error) {
	// If possible, disable IPv6 autoconfiguration on this interface so that
	// our RAs don't configure more IP addresses on this interface.
	prev, err := d.state.IPv6Autoconf(d.iface)
	if err != nil {
		return nil, fmt.Errorf("failed to get IPv6 autoconfiguration state on %q: %v", d.iface, err)
	}

	if err := d.state.SetIPv6Autoconf(d.iface, false); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			d.logf("permission denied while disabling IPv6 autoconfiguration, continuing anyway (try setting CAP_NET_ADMIN)")
		} else {
			return nil, fmt.Errorf("failed to disable IPv6 autoconfiguration on %q: %v", d.iface, err)
		}
	}

	restore := func() error {
		// If possible, restore the previous IPv6 autoconfiguration state.
		err := d.state.SetIPv6Autoconf(d.iface, prev)
		switch {
		case err == nil:
			// All good!
		case errors.Is(err, os.ErrPermission):
			// Continue anyway but provide a hint.
			d.logf("permission denied while restoring IPv6 autoconfiguration state, continuing anyway (try setting CAP_NET_ADMIN)")
		case errors.Is(err, os.ErrNotExist):
			// The interface may have been taken down due to system
			// reconfiguration, assume nothing needs to happen.
			d.logf("tried to restore IPv6 autoconfiguration state, but interface no longer exists, continuing anyway")
		default:
			// All other errors.
			return fmt.Errorf("failed to restore IPv6 autoconfiguration on %q: %v", d.iface, err)
		}

		return nil
	}

	return restore, nil
}

// logf prints a formatted log with the Dialer's interface name.
func (d *Dialer) logf(format string, v ...interface{}) {
	d.ll.Println(d.iface + ": " + fmt.Sprintf(format, v...))
}

// dialNDP creates an ndp.Conn which is ready to serve router advertisements.
func dialNDP(ifi *net.Interface) (*ndp.Conn, net.IP, error) {
	c, ip, err := ndp.Listen(ifi, ndp.LinkLocal)
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

	// We are now a router or want to examine messages as one would.
	if err := c.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return nil, nil, fmt.Errorf("failed to join IPv6 link-local all routers multicast group: %v", err)
	}

	return c, ip, nil
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
