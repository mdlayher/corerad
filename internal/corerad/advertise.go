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
	c        *ndp.Conn
	ifi      *net.Interface
	ip       net.IP
	autoPrev bool

	cfg config.Interface

	ll *log.Logger
	mm *AdvertiserMetrics
}

// NewAdvertiser creates an Advertiser for the specified interface. If ll is
// nil, logs are discarded. If mm is nil, metrics are discarded.
func NewAdvertiser(cfg config.Interface, ll *log.Logger, mm *AdvertiserMetrics) (*Advertiser, error) {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}
	if mm == nil {
		mm = NewAdvertiserMetrics(nil)
	}

	ifi, err := net.InterfaceByName(cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to look up interface %q: %v", cfg.Name, err)
	}

	// We can now initialize any plugins that rely on dynamic information
	// about the network interface.
	for _, p := range cfg.Plugins {
		if err := p.Prepare(ifi); err != nil {
			return nil, fmt.Errorf("failed to prepare plugin %q: %v", p.Name(), err)
		}
	}

	// If possible, disable IPv6 autoconfiguration on this interface so that
	// our RAs don't configure more IP addresses on this interface.
	autoPrev, err := setIPv6Autoconf(ifi.Name, false)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			ll.Printf("%s: permission denied while disabling IPv6 autoconfiguration, continuing anyway (try setting CAP_NET_ADMIN)", ifi.Name)
			mm.ErrorsTotal.WithLabelValues(ifi.Name, "configuration").Inc()
		} else {
			return nil, fmt.Errorf("failed to disable IPv6 autoconfiguration on %q: %v", ifi.Name, err)
		}
	}

	c, ip, err := ndp.Dial(ifi, ndp.LinkLocal)
	if err != nil {
		// Explicitly wrap this error for caller.
		return nil, fmt.Errorf("failed to create NDP listener: %w", err)
	}

	// We only want to accept router solicitation messages.
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)

	if err := c.SetICMPFilter(&f); err != nil {
		return nil, fmt.Errorf("failed to apply ICMPv6 filter: %v", err)
	}

	// We are now a router.
	if err := c.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return nil, fmt.Errorf("failed to join IPv6 link-local all routers multicast group: %v", err)
	}

	return &Advertiser{
		c:        c,
		ifi:      ifi,
		ip:       ip,
		autoPrev: autoPrev,

		cfg: cfg,

		ll: ll,
		mm: mm,
	}, nil
}

// A request indicates that a router advertisement should be sent to the
// specified IP address.
type request struct {
	IP net.IP
}

// Advertise begins router solicitation and advertisement handling. Advertise
// will block until ctx is canceled or an error occurs.
func (a *Advertiser) Advertise(ctx context.Context) error {
	// Attach the context to the errgroup so that goroutines are canceled when
	// one of them returns an error.
	eg, ctx := errgroup.WithContext(ctx)

	reqC := make(chan request, 16)

	// RA scheduler which consumes requests to send RAs and dispatches them
	// at the appropriate times.
	eg.Go(func() error {
		if err := a.schedule(ctx, reqC); err != nil {
			return fmt.Errorf("failed to schedule router advertisements: %v", err)
		}

		return nil
	})

	// Multicast RA generator.
	eg.Go(func() error {
		if err := a.multicast(ctx, reqC); err != nil {
			return fmt.Errorf("failed to multicast: %v", err)
		}

		return nil
	})

	// Listener which issues RAs in response to RS messages.
	eg.Go(func() error {
		if err := a.listen(ctx, reqC); err != nil {
			return fmt.Errorf("failed to listen: %v", err)
		}

		return nil
	})

	a.logf("initialized, sending router advertisements from %s", a.ip)

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to run advertiser: %v", err)
	}

	return a.shutdown()
}

// shutdown indicates to hosts that this host is no longer a router and restores
// the previous state of the interface.
func (a *Advertiser) shutdown() error {
	// In general, many of these actions are best-effort and should not halt
	// shutdown on failure.

	// Send a final router advertisement (TODO: more than one) with a router
	// lifetime of 0 to indicate that hosts should not use this router as a
	// default router, and then leave the all-routers group.
	a.cfg.DefaultLifetime = 0
	if err := a.send(net.IPv6linklocalallnodes); err != nil {
		a.logf("failed to send final multicast router advertisement: %v", err)
	}

	if err := a.c.LeaveGroup(net.IPv6linklocalallrouters); err != nil {
		a.logf("failed to leave IPv6 link-local all routers multicast group: %v", err)
	}

	if err := a.c.Close(); err != nil {
		a.logf("failed to stop NDP listener: %v", err)
	}

	// If possible, restore the previous IPv6 autoconfiguration state.
	if _, err := setIPv6Autoconf(a.ifi.Name, a.autoPrev); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			a.logf("permission denied while restoring IPv6 autoconfiguration state, continuing anyway (try setting CAP_NET_ADMIN)")
			a.mm.ErrorsTotal.WithLabelValues(a.cfg.Name, "configuration").Inc()
		} else {
			return fmt.Errorf("failed to restore IPv6 autoconfiguration on %q: %v", a.ifi.Name, err)
		}
	}

	return nil
}

// Constants taken from https://tools.ietf.org/html/rfc4861#section-10.
const (
	maxInitialAdvInterval = 16 * time.Second
	maxInitialAdv         = 3
	minDelayBetweenRAs    = 3 * time.Second
	maxRADelay            = 500 * time.Millisecond
)

// multicast runs a multicast advertising loop until ctx is canceled.
func (a *Advertiser) multicast(ctx context.Context, reqC chan<- request) error {
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
			return nil
		default:
		}

		reqC <- request{
			IP: net.IPv6linklocalallnodes,
		}

		select {
		case <-ctx.Done():
			return nil
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
			return fmt.Errorf("failed to interrupt listener: %v", err)
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

		m, _, host, err := a.c.ReadFrom()
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

			return fmt.Errorf("failed to read router solicitations: %v", err)
		}

		a.mm.MessagesReceivedTotal.WithLabelValues(a.cfg.Name, m.Type().String()).Add(1)

		if _, ok := m.(*ndp.RouterSolicitation); !ok {
			a.logf("received NDP message of type %T, ignoring", m)
			continue
		}

		// Issue a unicast RA.
		// TODO: consider checking for numerous RS in succession and issuing
		// a multicast RA in response.
		reqC <- request{IP: host}
	}
}

// schedule consumes RA requests and schedules them with workers so they may
// occur at the appropriate times.
func (a *Advertiser) schedule(ctx context.Context, reqC <-chan request) error {
	var (
		sg = schedgroup.New(ctx)

		prng          = rand.New(rand.NewSource(time.Now().UnixNano()))
		lastMulticast time.Time
	)

	for {
		// New request for each loop iteration to prevent races.
		var req request

		// Enable cancelation before sending any messages, if necessary.
		select {
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
				a.sendWorker(req.IP)
				return nil
			})
			continue
		}

		var delay time.Duration
		if lastMulticast.IsZero() {
			// We have not sent any multicast RAs; no delay.
			delay = 0
		} else {
			// Ensure that we space out multicast RAs as required by the RFC.
			delay = time.Since(lastMulticast)
			if delay < minDelayBetweenRAs {
				delay = minDelayBetweenRAs
			}
		}

		// Ready to send this multicast RA.
		lastMulticast = time.Now()
		sg.Delay(delay, func() error {
			a.sendWorker(req.IP)
			return nil
		})
	}
}

// sendWorker is a goroutine worker which sends a router advertisement to ip.
func (a *Advertiser) sendWorker(ip net.IP) {
	busy := a.mm.SchedulerWorkers.WithLabelValues(a.cfg.Name)
	busy.Inc()
	defer busy.Dec()

	if err := a.send(ip); err != nil {
		a.logf("failed to send scheduled router advertisement to %s: %v", ip, err)
		a.mm.ErrorsTotal.WithLabelValues(a.cfg.Name, "transmit").Inc()

		// TODO: figure out which errors are recoverable or not.
		return
	}

	typ := "unicast"
	if ip.IsMulticast() {
		typ = "multicast"
		a.mm.LastMulticastTime.WithLabelValues(a.cfg.Name).SetToCurrentTime()
	}

	a.mm.RouterAdvertisementsTotal.WithLabelValues(a.cfg.Name, typ).Add(1)
}

// send sends a single router advertisement to the destination IP address,
// which may be a unicast or multicast address.
func (a *Advertiser) send(dst net.IP) error {
	// Build a router advertisement from configuration and always append
	// the source address option.
	ra, err := a.buildRA(a.cfg)
	if err != nil {
		return fmt.Errorf("failed to build router advertisement: %v", err)
	}

	// If the interface is not forwarding packets, we must set the router
	// lifetime field to zero, per:
	//  https://tools.ietf.org/html/rfc4861#section-6.2.5.
	forwarding, err := getIPv6Forwarding(a.ifi.Name)
	if err != nil {
		return fmt.Errorf("failed to get IPv6 forwarding state: %v", err)
	}
	if !forwarding {
		ra.RouterLifetime = 0
	}

	if err := a.c.WriteTo(ra, nil, dst); err != nil {
		return fmt.Errorf("failed to send router advertisement to %s: %v", dst, err)
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

	// TODO plugin cancelation.
	var err error
	for _, p := range ifi.Plugins {
		ra, err = p.Apply(ra)
		if err != nil {
			return nil, err
		}
	}

	// TODO: apparently it is also valid to omit this, but we can think
	// about that later.
	ra.Options = append(ra.Options, &ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      a.ifi.HardwareAddr,
	})

	return ra, nil
}

// logf prints a formatted log with the Advertiser's interface name.
func (a *Advertiser) logf(format string, v ...interface{}) {
	a.ll.Println(a.ifi.Name + ": " + fmt.Sprintf(format, v...))
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
