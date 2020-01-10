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

package corerad

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/ndp"
	"github.com/mdlayher/schedgroup"
	"golang.org/x/net/ipv6"
	"golang.org/x/sync/errgroup"
)

// An Advertiser sends NDP router advertisements.
type Advertiser struct {
	// Static configuration.
	iface string
	cfg   config.Interface
	ll    *log.Logger
	mm    *AdvertiserMetrics

	// Dynamic configuration, set up on each (re)initialization.
	c   conn
	mac net.HardwareAddr

	// Test hooks.
	dial              func(ifi *net.Interface) (conn, net.IP, error)
	checkInterface    func(iface string) (*net.Interface, error)
	getIPv6Forwarding func(iface string) (bool, error)
	getIPv6Autoconf   func(iface string) (bool, error)
	setIPv6Autoconf   func(iface string, enable bool) error

	// Notifications of internal state change for tests.
	reinitC chan struct{}
}

// A conn is a wrapper around *ndp.Conn which enables simulated testing.
type conn interface {
	Close() error
	JoinGroup(group net.IP) error
	LeaveGroup(group net.IP) error
	ReadFrom() (ndp.Message, *ipv6.ControlMessage, net.IP, error)
	SetControlMessage(flags ipv6.ControlFlags, on bool) error
	SetICMPFilter(f *ipv6.ICMPFilter) error
	SetReadDeadline(t time.Time) error
	WriteTo(m ndp.Message, cm *ipv6.ControlMessage, dst net.IP) error
}

// NewAdvertiser creates an Advertiser for the specified interface. If ll is
// nil, logs are discarded. If mm is nil, metrics are discarded.
func NewAdvertiser(iface string, cfg config.Interface, ll *log.Logger, mm *AdvertiserMetrics) *Advertiser {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}
	if mm == nil {
		mm = NewAdvertiserMetrics(nil)
	}

	return &Advertiser{
		iface: iface,
		cfg:   cfg,
		ll:    ll,
		mm:    mm,

		// c and ifi are initialized in a.init.

		// Configure actual system parameters by default.
		dial: func(ifi *net.Interface) (conn, net.IP, error) {
			return ndp.Dial(ifi, ndp.LinkLocal)
		},
		checkInterface:    checkInterface,
		getIPv6Forwarding: getIPv6Forwarding,
		getIPv6Autoconf:   getIPv6Autoconf,
		setIPv6Autoconf:   setIPv6Autoconf,

		reinitC: make(chan struct{}),
	}
}

// A request indicates that a router advertisement should be sent to the
// specified IP address.
type request struct {
	IP net.IP
}

// Advertise initializes the configured interface and begins router solicitation
// and advertisement handling. Advertise will block until ctx is canceled or an
// error occurs.
func (a *Advertiser) Advertise(ctx context.Context) error {
	// Attempt immediate initialization and fall back to reinit loop if that
	// does not succeed.
	autoPrev, err := a.init()
	if err != nil {
		autoPrev, err = a.reinit(ctx, err)
		if err != nil {
			// Don't block user shutdown.
			if errors.Is(err, context.Canceled) {
				return nil
			}

			return fmt.Errorf("failed to initialize %q advertiser: %w", a.iface, err)
		}
	}

	for {
		err := a.advertise(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			// Intentional shutdown.
			return a.shutdown(autoPrev)
		case err == nil:
			panic("corerad: advertise must never return nil error")
		}

		// We encountered an error. Try to reinitialize the Advertiser based
		// on whether or not the error is deemed recoverable.
		a.logf("error advertising, attempting to reinitialize")

		autoPrev, err = a.reinit(ctx, err)
		if err != nil {
			// Don't block user shutdown.
			if errors.Is(err, context.Canceled) {
				return nil
			}

			return fmt.Errorf("failed to reinitialize %q advertiser: %v", a.iface, err)
		}
	}
}

// advertise is the internal loop for Advertise which coordinates the various
// Advertiser goroutines.
func (a *Advertiser) advertise(ctx context.Context) error {
	// Attach the context to the errgroup so that goroutines are canceled when
	// one of them returns an error.
	eg, ctx := errgroup.WithContext(ctx)

	reqC := make(chan request, 16)

	// RA scheduler which consumes requests to send RAs and dispatches them
	// at the appropriate times.
	eg.Go(func() error {
		if err := a.schedule(ctx, reqC); err != nil {
			return fmt.Errorf("failed to schedule router advertisements: %w", err)
		}

		return nil
	})

	// Multicast RA generator.
	eg.Go(func() error {
		a.multicast(ctx, reqC)
		return nil
	})

	// Listener which issues RAs in response to RS messages.
	eg.Go(func() error {
		if err := a.listen(ctx, reqC); err != nil {
			return fmt.Errorf("failed to listen: %w", err)
		}

		return nil
	})

	// TODO: link state watcher which returns errors for any state change,
	// forcing these goroutines to cancel and reinitialize.

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to run advertiser: %w", err)
	}

	// Should only reach this state when context is canceled on shutdown.
	return ctx.Err()
}

// Constants taken from https://tools.ietf.org/html/rfc4861#section-10.
const (
	maxInitialAdvInterval = 16 * time.Second
	maxInitialAdv         = 3
	minDelayBetweenRAs    = 3 * time.Second
	maxRADelay            = 500 * time.Millisecond
)

// multicast runs a multicast advertising loop until ctx is canceled.
func (a *Advertiser) multicast(ctx context.Context, reqC chan<- request) {
	// Initialize PRNG so we can add jitter to our unsolicited multicast RA
	// delay times.
	var (
		prng = rand.New(rand.NewSource(time.Now().UnixNano()))
		min  = a.cfg.MinInterval.Nanoseconds()
		max  = a.cfg.MaxInterval.Nanoseconds()
	)

	for i := 0; ; i++ {
		// Enable cancelation before sending any messages, if necessary.
		select {
		case <-ctx.Done():
			return
		default:
		}

		reqC <- request{IP: net.IPv6linklocalallnodes}

		select {
		case <-ctx.Done():
			return
		case <-time.After(multicastDelay(prng, i, min, max)):
		}
	}
}

// deadlineNow causes connection deadlines to trigger immediately.
var deadlineNow = time.Unix(1, 0)

// listen issues unicast router advertisements in response to router
// solicitations, until ctx is canceled.
func (a *Advertiser) listen(ctx context.Context, reqC chan<- request) error {
	// Wait for cancelation and then force any pending reads to time out.
	var eg errgroup.Group
	eg.Go(func() error {
		<-ctx.Done()

		if err := a.c.SetReadDeadline(deadlineNow); err != nil {
			return fmt.Errorf("failed to interrupt listener: %w", err)
		}

		return nil
	})

	for {
		// Enable cancelation before sending any messages, if necessary.
		select {
		case <-ctx.Done():
			return eg.Wait()
		default:
		}

		m, cm, host, err := a.c.ReadFrom()
		if err != nil {
			if ctx.Err() != nil {
				// Context canceled.
				return eg.Wait()
			}

			a.mm.ErrorsTotal.WithLabelValues(a.cfg.Name, "receive").Inc()

			if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
				// Temporary error or timeout, just continue.
				// TODO: smarter backoff/retry.
				time.Sleep(50 * time.Millisecond)
				continue
			}

			return fmt.Errorf("failed to read router solicitations: %w", err)
		}

		a.mm.MessagesReceivedTotal.WithLabelValues(a.cfg.Name, m.Type().String()).Add(1)

		if _, ok := m.(*ndp.RouterSolicitation); !ok {
			a.logf("received NDP message of type %T from %s, ignoring", m, host)
			a.mm.MessagesReceivedInvalidTotal.WithLabelValues(a.cfg.Name, m.Type().String()).Add(1)
			continue
		}

		// Ensure this message has a valid hop limit.
		if cm.HopLimit != ndp.HopLimit {
			a.logf("received NDP message with IPv6 hop limit %d from %s, ignoring", cm.HopLimit, host)
			a.mm.MessagesReceivedInvalidTotal.WithLabelValues(a.cfg.Name, m.Type().String()).Add(1)
			continue
		}

		// Issue a unicast RA for clients with valid addresses, or a multicast
		// RA for any client contacting us via the IPv6 unspecified address,
		// per https://tools.ietf.org/html/rfc4861#section-6.2.6.
		// TODO: see if it's possible to make Linux send from an unspecified
		// address so we can test this fully.
		if host.Equal(net.IPv6unspecified) {
			host = net.IPv6linklocalallnodes
		}

		// TODO: consider checking for numerous RS in succession and issuing
		// a multicast RA in response.
		reqC <- request{IP: host}
	}
}

// schedule consumes RA requests and schedules them with workers so they may
// occur at the appropriate times.
func (a *Advertiser) schedule(ctx context.Context, reqC <-chan request) error {
	// Enable canceling schedule's context on send RA error.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		// Schedule router advertisements and handles any errors from those
		// advertisements. Note that sg.Wait cannot be used for that purpose
		// because it is invalid to call Wait and schedule other work after,
		// meaning that we can only Wait once we're exiting this function.
		sg   = schedgroup.New(ctx)
		errC = make(chan error)

		prng          = rand.New(rand.NewSource(time.Now().UnixNano()))
		lastMulticast time.Time
	)

	for {
		// New request for each loop iteration to prevent races.
		var req request

		select {
		case err := <-errC:
			// We received an error and will need to determine if we can
			// reinitialize the listener. Don't schedule any more tasks and
			// return the error immediately.
			cancel()
			_ = sg.Wait()
			return err
		case <-ctx.Done():
			// Context cancelation is expected.
			if err := sg.Wait(); err != nil && err != context.Canceled {
				return err
			}

			return nil
		case req = <-reqC:
		}

		if !req.IP.IsMulticast() {
			// This is a unicast RA. Delay it for a short period of time per
			// the RFC and then send it.
			delay := time.Duration(prng.Int63n(maxRADelay.Nanoseconds())) * time.Nanosecond
			sg.Delay(delay, func() error {
				if err := a.sendWorker(req.IP); err != nil {
					errC <- err
				}
				return nil
			})
			continue
		}

		// Ensure that we space out multicast RAs as required by the RFC.
		var delay time.Duration
		if !lastMulticast.IsZero() && time.Since(lastMulticast) < minDelayBetweenRAs {
			delay = minDelayBetweenRAs
		}

		// Ready to send this multicast RA.
		lastMulticast = time.Now()
		sg.Delay(delay, func() error {
			if err := a.sendWorker(req.IP); err != nil {
				errC <- err
			}
			return nil
		})
	}
}

// sendWorker is a goroutine worker which sends a router advertisement to ip.
func (a *Advertiser) sendWorker(ip net.IP) error {
	busy := a.mm.SchedulerWorkers.WithLabelValues(a.cfg.Name)
	busy.Inc()
	defer busy.Dec()

	if err := a.send(ip, a.cfg); err != nil {
		a.logf("failed to send scheduled router advertisement to %s: %v", ip, err)
		a.mm.ErrorsTotal.WithLabelValues(a.cfg.Name, "transmit").Inc()
		return err
	}

	typ := "unicast"
	if ip.IsMulticast() {
		typ = "multicast"
		a.mm.LastMulticastTime.WithLabelValues(a.cfg.Name).SetToCurrentTime()
	}

	a.mm.RouterAdvertisementsTotal.WithLabelValues(a.cfg.Name, typ).Add(1)
	return nil
}

// send sends a single router advertisement built from cfg to the destination IP
// address, which may be a unicast or multicast address.
func (a *Advertiser) send(dst net.IP, cfg config.Interface) error {
	// Build a router advertisement from configuration and always append
	// the source address option.
	ra, err := a.buildRA(cfg)
	if err != nil {
		return fmt.Errorf("failed to build router advertisement: %w", err)
	}

	// If the interface is not forwarding packets, we must set the router
	// lifetime field to zero, per:
	//  https://tools.ietf.org/html/rfc4861#section-6.2.5.
	forwarding, err := a.getIPv6Forwarding(a.iface)
	if err != nil {
		return fmt.Errorf("failed to get IPv6 forwarding state: %w", err)
	}
	if !forwarding {
		ra.RouterLifetime = 0
	}

	if err := a.c.WriteTo(ra, nil, dst); err != nil {
		return fmt.Errorf("failed to send router advertisement to %s: %w", dst, err)
	}

	return nil
}

// buildRA builds a router advertisement from configuration and applies any
// necessary plugins.
func (a *Advertiser) buildRA(ifi config.Interface) (*ndp.RouterAdvertisement, error) {
	ra := &ndp.RouterAdvertisement{
		CurrentHopLimit:      ifi.HopLimit,
		ManagedConfiguration: ifi.Managed,
		OtherConfiguration:   ifi.OtherConfig,
		RouterLifetime:       ifi.DefaultLifetime,
		ReachableTime:        ifi.ReachableTime,
		RetransmitTimer:      ifi.RetransmitTimer,
	}

	for _, p := range ifi.Plugins {
		if err := p.Apply(ra); err != nil {
			return nil, fmt.Errorf("failed to apply plugin %q: %v", p.Name(), err)
		}
	}

	// Apply MTU option if set.
	if ifi.MTU != 0 {
		ra.Options = append(ra.Options, ndp.NewMTU(uint32(ifi.MTU)))
	}

	// TODO: apparently it is also valid to omit this, but we can think
	// about that later.
	ra.Options = append(ra.Options, &ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      a.mac,
	})

	return ra, nil
}

// init initializes the Advertiser in preparation for handling NDP traffic.
func (a *Advertiser) init() (bool, error) {
	// Verify the interface is available and ready for listening.
	ifi, err := a.checkInterface(a.iface)
	if err != nil {
		return false, err
	}

	// Initialize the NDP listener.
	c, ip, err := a.dial(ifi)
	if err != nil {
		return false, fmt.Errorf("failed to create NDP listener: %v", err)
	}

	// We can now initialize any plugins that rely on dynamic information
	// about the network interface.
	for _, p := range a.cfg.Plugins {
		if err := p.Prepare(ifi); err != nil {
			return false, fmt.Errorf("failed to prepare plugin %q: %v", p.Name(), err)
		}
	}

	// If possible, disable IPv6 autoconfiguration on this interface so that
	// our RAs don't configure more IP addresses on this interface.
	autoPrev, err := a.getIPv6Autoconf(a.iface)
	if err != nil {
		return false, fmt.Errorf("failed to get IPv6 autoconfiguration state on %q: %v", a.iface, err)
	}

	if err := a.setIPv6Autoconf(a.iface, false); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			a.logf("permission denied while disabling IPv6 autoconfiguration, continuing anyway (try setting CAP_NET_ADMIN)")
			a.mm.ErrorsTotal.WithLabelValues(a.iface, "configuration").Inc()
		} else {
			return false, fmt.Errorf("failed to disable IPv6 autoconfiguration on %q: %v", a.iface, err)
		}
	}

	// We only want to accept router solicitation messages.
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)

	if err := c.SetICMPFilter(&f); err != nil {
		return false, fmt.Errorf("failed to apply ICMPv6 filter: %v", err)
	}

	// Enable inspection of IPv6 control messages.
	flags := ipv6.FlagHopLimit
	if err := c.SetControlMessage(flags, true); err != nil {
		return false, fmt.Errorf("failed to apply IPv6 control message flags: %v", err)
	}

	// We are now a router.
	if err := c.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return false, fmt.Errorf("failed to join IPv6 link-local all routers multicast group: %v", err)
	}

	a.c = c
	a.mac = ifi.HardwareAddr

	a.logf("initialized, advertising from %s", ip)

	return autoPrev, nil
}

// reinit attempts repeated reinitialization of the Advertiser based on whether
// the input error is considered recoverbale.
func (a *Advertiser) reinit(ctx context.Context, err error) (bool, error) {
	// Notify the beginning and end of reinit logic to anyone listening.
	notify := func() {
		select {
		case a.reinitC <- struct{}{}:
		default:
		}
	}

	notify()
	defer notify()

	// Check for conditions which are recoverable.
	funcs := []func(err error) bool{
		func(err error) bool {
			// TODO: inspect syscall error numbers, but for now, treat
			// these errors as recoverable.
			var serr *os.SyscallError
			return errors.As(err, &serr)
		},
		func(err error) bool { return errors.Is(err, errLinkNotReady) },
	}

	var canReinit bool
	for _, fn := range funcs {
		if fn(err) {
			// Error is recoverable.
			canReinit = true
			break
		}
	}
	if !canReinit {
		// Unrecoverable error.
		return false, err
	}

	// Recoverable error, try to initialize for delay*attempts seconds, every
	// delay seconds.
	const (
		attempts = 10
		delay    = 3 * time.Second
	)

	for i := 0; i < attempts; i++ {
		// Don't wait on the first attempt.
		if i != 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(delay):
			}
		}

		autoPrev, err := a.init()
		if err != nil {
			a.logf("retrying initialization, %d attempts remaining", attempts-(i+1))
			continue
		}

		return autoPrev, nil
	}

	return false, fmt.Errorf("timed out trying to initialize after error: %v", err)
}

// shutdown indicates to hosts that this host is no longer a router and restores
// the previous state of the interface.
func (a *Advertiser) shutdown(autoPrev bool) error {
	// In general, many of these actions are best-effort and should not halt
	// shutdown on failure.

	// Send a final router advertisement (TODO: more than one) with a router
	// lifetime of 0 to indicate that hosts should not use this router as a
	// default router, and then leave the all-routers group.
	//
	// a.cfg is copied in case any delayed send workers are outstanding and
	// the server's context is canceled.
	cfg := a.cfg
	cfg.DefaultLifetime = 0

	if err := a.send(net.IPv6linklocalallnodes, cfg); err != nil {
		a.logf("failed to send final multicast router advertisement: %v", err)
	}

	if err := a.c.LeaveGroup(net.IPv6linklocalallrouters); err != nil {
		a.logf("failed to leave IPv6 link-local all routers multicast group: %v", err)
	}

	if err := a.c.Close(); err != nil {
		a.logf("failed to stop NDP listener: %v", err)
	}

	// If possible, restore the previous IPv6 autoconfiguration state.
	if err := a.setIPv6Autoconf(a.iface, autoPrev); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			a.logf("permission denied while restoring IPv6 autoconfiguration state, continuing anyway (try setting CAP_NET_ADMIN)")
			a.mm.ErrorsTotal.WithLabelValues(a.cfg.Name, "configuration").Inc()
		} else {
			return fmt.Errorf("failed to restore IPv6 autoconfiguration on %q: %v", a.iface, err)
		}
	}

	return nil
}

// logf prints a formatted log with the Advertiser's interface name.
func (a *Advertiser) logf(format string, v ...interface{}) {
	a.ll.Println(a.iface + ": " + fmt.Sprintf(format, v...))
}

// multicastDelay selects an appropriate delay duration for unsolicited
// multicast RA sending.
func multicastDelay(r *rand.Rand, i int, min, max int64) time.Duration {
	// Implements the algorithm described in:
	// https://tools.ietf.org/html/rfc4861#section-6.2.4.

	var d time.Duration
	if min == max {
		// Identical min/max, use a static interval.
		d = (time.Duration(max) * time.Nanosecond).Round(time.Second)
	} else {
		// min <= wait <= max, rounded to 1 second granularity.
		d = (time.Duration(min+rand.Int63n(max-min)) * time.Nanosecond).Round(time.Second)
	}

	// For first few advertisements, select a shorter wait time so routers
	// can be discovered quickly, per the RFC.
	if i < maxInitialAdv && d > maxInitialAdvInterval {
		d = maxInitialAdvInterval
	}

	return d
}

// errLinkNotReady is a sentinel which indicates an interface is not ready
// for use with an Advertiser.
var errLinkNotReady = errors.New("link not ready")

// checkInterface verifies the readiness of an interface.
func checkInterface(iface string) (*net.Interface, error) {
	// Link must exist.
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("failed to get interface %q: %w", ifi.Name, err)
	}

	// Link must have a MAC address (e.g. WireGuard links do not).
	if ifi.HardwareAddr == nil {
		return nil, fmt.Errorf("interface %q has no MAC address", iface)
	}

	// Link must be up.
	// TODO: check point-to-point and multicast flags and configure accordingly.
	if ifi.Flags&net.FlagUp == 0 {
		return nil, errLinkNotReady
	}

	// Link must have an IPv6 link-local unicast address.
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get interface addresses: %w", err)
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
		return nil, errLinkNotReady
	}

	return ifi, nil
}

// isIPv6 determines if ip is an IPv6 address.
func isIPv6(ip net.IP) bool {
	return ip.To16() != nil && ip.To4() == nil
}
