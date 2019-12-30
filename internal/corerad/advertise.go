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
	b   *builder

	ll *log.Logger
}

// NewAdvertiser creates an Advertiser for the specified interface. If ll is
// nil, logs are discarded.
func NewAdvertiser(cfg config.Interface, ll *log.Logger) (*Advertiser, error) {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	ifi, err := net.InterfaceByName(cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to look up interface %q: %v", cfg.Name, err)
	}

	// If possible, disable IPv6 autoconfiguration on this interface so that
	// our RAs don't configure more IP addresses on this interface.
	autoPrev, err := setIPv6Autoconf(ifi.Name, false)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			ll.Printf("%s: permission denied while disabling IPv6 autoconfiguration, continuing anyway (try setting CAP_NET_ADMIN)", ifi.Name)
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
		// Set up a builder to construct RAs from configuration.
		b: &builder{
			// Fetch the configured interface's addresses.
			Addrs: ifi.Addrs,
		},

		ll: ll,
	}, nil
}

// Advertise begins router solicitation and advertisement handling. Advertise
// will block until ctx is canceled or an error occurs.
func (a *Advertiser) Advertise(ctx context.Context) error {
	// Attach the context to the errgroup so that goroutines are canceled when
	// one of them returns an error.
	eg, ctx := errgroup.WithContext(ctx)

	// Multicast RA sender.
	eg.Go(func() error {
		if err := a.multicast(ctx); err != nil {
			return fmt.Errorf("failed to multicast: %v", err)
		}

		return nil
	})

	// RS listener which also issues unicast RAs.
	eg.Go(func() error {
		if err := a.listen(ctx); err != nil {
			return fmt.Errorf("failed to listen: %v", err)
		}

		return nil
	})

	a.logf("initialized, sending router advertisements from %s", a.ip)

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to run advertiser: %v", err)
	}

	return a.cleanup()
}

// cleanup restores the previous state of the interface and cleans up the
// Advertiser's internal connections.
func (a *Advertiser) cleanup() error {
	if err := a.c.Close(); err != nil {
		// Warn, but don't halt cleanup.
		a.logf("failed to stop NDP listener: %v", err)
	}

	// If possible, restore the previous IPv6 autoconfiguration state.
	if _, err := setIPv6Autoconf(a.ifi.Name, a.autoPrev); err != nil {
		if errors.Is(err, os.ErrPermission) {
			// Continue anyway but provide a hint.
			a.logf("permission denied while restoring IPv6 autoconfiguration state, continuing anyway (try setting CAP_NET_ADMIN)")
		} else {
			return fmt.Errorf("failed to restore IPv6 autoconfiguration on %q: %v", a.ifi.Name, err)
		}
	}

	return nil
}

// multicast runs a multicast advertising loop until ctx is canceled.
func (a *Advertiser) multicast(ctx context.Context) error {
	// Initialize PRNG so we can add jitter to our unsolicited multicast RA
	// delay times, per the RFC:
	// https://tools.ietf.org/html/rfc4861#section-6.2.4.
	var (
		rand = rand.New(rand.NewSource(time.Now().UnixNano()))
		min  = a.cfg.MinInterval.Nanoseconds()
		max  = a.cfg.MaxInterval.Nanoseconds()
	)

	for {
		// Enable cancelation before sending any messages, if necessary.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if err := a.send(net.IPv6linklocalallnodes); err != nil {
			return fmt.Errorf("failed to send multicast router advertisement: %v", err)
		}

		var wait time.Duration
		if min == max {
			// Identical min/max, use a static interval.
			wait = (time.Duration(max) * time.Nanosecond).Round(time.Second)
		} else {
			// min <= wait <= max, rounded to 1 second granularity.
			wait = (time.Duration(min+rand.Int63n(max-min)) * time.Nanosecond).Round(time.Second)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// deadlineNow causes connection deadlines to trigger immediately.
var deadlineNow = time.Unix(1, 0)

// listen issues unicast router advertisements in response to router
// solicitations, until ctx is canceled.
func (a *Advertiser) listen(ctx context.Context) error {
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
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
				// Temporary error or timeout, just continue.
				continue
			}

			return fmt.Errorf("failed to read router solicitations: %v", err)
		}

		rs, ok := m.(*ndp.RouterSolicitation)
		if !ok {
			a.logf("received NDP message of type %T, ignoring", m)
			continue
		}

		// TODO: metrics for RS fields.
		_ = rs

		if err := a.send(host); err != nil {
			return fmt.Errorf("failed to send unicast router advertisement: %v", err)
		}
	}
}

// send sends a single router advertisement to the destination IP address,
// which may be a unicast or multicast address.
func (a *Advertiser) send(dst net.IP) error {
	// Build a router advertisement from configuration and always append
	// the source address option.
	ra, err := a.b.Build(a.cfg)
	if err != nil {
		return fmt.Errorf("failed to build router advertisement: %v", err)
	}

	// TODO: apparently it is also valid to omit this, but we can think
	// about that later.
	ra.Options = append(ra.Options, &ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      a.ifi.HardwareAddr,
	})

	if err := a.c.WriteTo(ra, nil, dst); err != nil {
		return fmt.Errorf("failed to send router advertisement to %s: %v", dst, err)
	}

	return nil
}

func (a *Advertiser) logf(format string, v ...interface{}) {
	a.ll.Println(a.ifi.Name + ": " + fmt.Sprintf(format, v...))
}
