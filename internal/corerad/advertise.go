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
	"golang.org/x/sync/errgroup"
)

// errLinkChange is a sentinel value which indicates a link state change.
var errLinkChange = errors.New("link state change")

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

	// Notifications of internal state change for tests.
	reinitC chan struct{}
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

		// By default, directly manipulate the system.
		c: newSystemConn(ll, mm),

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
// error occurs. If watchC is not nil, it will be used to trigger the
// reinitialization process. Typically watchC is used with the Watcher type.
func (a *Advertiser) Advertise(ctx context.Context, watchC <-chan struct{}) error {
	// Attempt immediate initialization and fall back to reinit loop if that
	// does not succeed.
	if err := a.init(); err != nil {
		if err := a.reinit(ctx, err); err != nil {
			// Don't block user shutdown.
			if errors.Is(err, context.Canceled) {
				return nil
			}

			return fmt.Errorf("failed to initialize %q advertiser: %w", a.iface, err)
		}
	}

	for {
		err := a.advertise(ctx, watchC)
		switch {
		case errors.Is(err, context.Canceled):
			// Intentional shutdown.
			return a.shutdown()
		case err == nil:
			panic("corerad: advertise must never return nil error")
		}

		// We encountered an error. Try to reinitialize the Advertiser based
		// on whether or not the error is deemed recoverable.
		if err := a.reinit(ctx, err); err != nil {
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
func (a *Advertiser) advertise(ctx context.Context, watchC <-chan struct{}) error {
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

	// Multicast RA generator, unless running in unicast-only mode.
	if !a.cfg.UnicastOnly {
		eg.Go(func() error {
			a.multicast(ctx, reqC)
			return nil
		})
	}

	// Listener which issues RAs in response to RS messages.
	eg.Go(func() error {
		if err := a.listen(ctx, reqC); err != nil {
			return fmt.Errorf("failed to listen: %w", err)
		}

		return nil
	})

	// Link state watcher, unless no watch channel was specified.
	if watchC != nil {
		eg.Go(func() error {
			select {
			case <-ctx.Done():
				return nil
			case _, ok := <-watchC:
				if !ok {
					// Watcher halted.
					return nil
				}

				// Watcher indicated a state change.
				return errLinkChange
			}
		})
	}

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

		// Jitter for RA delays.
		prng = rand.New(rand.NewSource(time.Now().UnixNano()))

		// Assume that a.init sent the initial RA recently and space out others
		// accordingly.
		lastMulticast = time.Now()
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
		if time.Since(lastMulticast) < minDelayBetweenRAs {
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
	if cfg.UnicastOnly && dst.IsMulticast() {
		// Nothing to do.
		return nil
	}

	// Build a router advertisement from configuration and always append
	// the source address option.
	ra, err := a.buildRA(cfg)
	if err != nil {
		return fmt.Errorf("failed to build router advertisement: %w", err)
	}

	// If the interface is not forwarding packets, we must set the router
	// lifetime field to zero, per:
	//  https://tools.ietf.org/html/rfc4861#section-6.2.5.
	forwarding, err := a.c.IPv6Forwarding()
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

	return ra, nil
}

// init initializes the Advertiser in preparation for handling NDP traffic.
func (a *Advertiser) init() error {
	// Verify the interface is available and ready for listening.
	ifi, ip, err := a.c.Dial(a.iface)
	if err != nil {
		return err
	}

	// We can now initialize any plugins that rely on dynamic information
	// about the network interface.
	for _, p := range a.cfg.Plugins {
		if err := p.Prepare(ifi); err != nil {
			return fmt.Errorf("failed to prepare plugin %q: %v", p.Name(), err)
		}
	}

	a.mac = ifi.HardwareAddr

	// Before starting any other goroutines, verify that the interface can
	// actually be used to send an initial router advertisement, avoiding a
	// needless start/error/restart loop.
	if err := a.send(net.IPv6linklocalallnodes, a.cfg); err != nil {
		return fmt.Errorf("failed to send initial multicast router advertisement: %v", err)
	}

	// Note unicast-only mode in logs.
	var method string
	if a.cfg.UnicastOnly {
		method = "unicast-only "
	}

	a.logf("initialized, advertising %sfrom %s", method, ip)

	return nil
}

// reinit attempts repeated reinitialization of the Advertiser based on whether
// the input error is considered recoverbale.
func (a *Advertiser) reinit(ctx context.Context, err error) error {
	// Notify progress in reinit logic to anyone listening.
	notify := func() {
		select {
		case a.reinitC <- struct{}{}:
		default:
		}
	}

	defer notify()

	// Check for conditions which are recoverable.
	var serr *os.SyscallError
	switch {
	case errors.As(err, &serr):
		// TODO: check for certain syscall error numbers.
		a.logf("error advertising, reinitializing")
	case errors.Is(err, errLinkNotReady):
		a.logf("interface not ready, reinitializing")
	case errors.Is(err, errLinkChange):
		a.logf("interface state changed, reinitializing")
	default:
		// Unrecoverable error
		return err
	}

	// Recoverable error, try to initialize for delay*attempts seconds, every
	// delay seconds.
	const (
		attempts = 40
		delay    = 3 * time.Second
	)

	for i := 0; i < attempts; i++ {
		// Notify of reinit on each attempt.
		notify()

		// Don't wait on the first attempt.
		if i != 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		if err := a.init(); err != nil {
			a.logf("retrying initialization, %d attempt(s) remaining: %v", attempts-(i+1), err)
			continue
		}

		return nil
	}

	return fmt.Errorf("timed out trying to initialize after error: %v", err)
}

// shutdown indicates to hosts that this host is no longer a router and restores
// the previous state of the interface.
func (a *Advertiser) shutdown() error {
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

	if err := a.c.Close(); err != nil {
		a.logf("failed to stop NDP listener: %v", err)
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
