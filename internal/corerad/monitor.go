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
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"time"

	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/ndp"
	"github.com/mdlayher/netstate"
	"golang.org/x/sync/errgroup"
	"inet.af/netaddr"
)

// A Monitor listens and reports on NDP traffic.
type Monitor struct {
	// OnMessage is an optional callback which will fire when the monitor
	// receives an NDP message.
	OnMessage func(m ndp.Message)

	// Static configuration.
	iface string
	ll    *log.Logger
	mm    *Metrics

	// Socket creation and system state manipulation.
	dialer *system.Dialer
	state  system.State

	// now allows overriding the current time.
	now func() time.Time
}

// NewMonitor creates a Monitor for the specified interface. If ll is nil, logs
// are discarded. If mm is nil, metrics are discarded.
func NewMonitor(
	iface string,
	dialer *system.Dialer,
	state system.State,
	ll *log.Logger,
	mm *Metrics,
) *Monitor {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}
	if mm == nil {
		mm = NewMetrics(nil, nil, nil)
	}

	return &Monitor{
		iface:  iface,
		ll:     ll,
		mm:     mm,
		dialer: dialer,
		state:  state,

		// By default use real time.
		now: time.Now,
	}
}

// Monitor initializes the configured interface and listening and reporting on
// incoming NDP traffic. Monitor will block until ctx is canceled or an error
// occurs.
func (m *Monitor) Monitor(ctx context.Context, watchC <-chan netstate.Change) error {
	return m.dialer.Dial(ctx, func(ctx context.Context, dctx *system.DialContext) error {
		m.logf("initialized, monitoring from %s", dctx.IP)

		// Monitor until an error occurs, reinitializing under certain
		// circumstances.
		err := m.monitor(ctx, dctx.Conn, watchC)
		switch {
		case errors.Is(err, context.Canceled):
			// Intentional shutdown.
			return nil
		case err == nil:
			panic("corerad: monitor must never return nil error")
		default:
			return err
		}
	})
}

// monitor is the internal loop for Monitor which coordinates the various
// Monitor goroutines.
func (m *Monitor) monitor(ctx context.Context, conn system.Conn, watchC <-chan netstate.Change) error {
	// Attach the context to the errgroup so that goroutines are canceled when
	// one of them returns an error.
	eg, ctx := errgroup.WithContext(ctx)

	// Listener which listens for and reports on NDP traffic.
	eg.Go(func() error {
		if err := m.listen(ctx, conn); err != nil {
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
					// Watcher halted or not available on this OS.
					return nil
				}

				// TODO: inspect for specific state changes.

				// Watcher indicated a state change.
				return system.ErrLinkChange
			}
		})
	}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to run Monitor: %w", err)
	}

	// Should only reach this state when context is canceled on shutdown.
	return ctx.Err()
}

// listen issues for incoming NDP messages until ctx is canceled.
func (m *Monitor) listen(ctx context.Context, conn system.Conn) error {
	// Wait for cancelation and then force any pending reads to time out.
	var eg errgroup.Group
	eg.Go(interruptContext(ctx, conn))

	for {
		// Receive and analyze incoming NDP messages.
		msg, _, host, err := receiveRetry(ctx, conn)
		if err != nil {
			return fmt.Errorf("failed to read NDP messages: %w", err)
		}

		// TODO(mdlayher): consider adding a verbose mode and hiding some/all
		// of these logs.
		m.logf("monitor received %q from %s", msg.Type(), host)

		m.mm.MonitorMessagesReceivedTotal(m.iface, host.String(), msg.Type().String())

		// TODO(mdlayher): expand type switch.
		switch msg := msg.(type) {
		case *ndp.RouterAdvertisement:
			m.raMetrics(msg, host)
		}

		// Callback must fire after logging/metrics to ensure they are consistent
		// in tests.
		if m.OnMessage != nil {
			m.OnMessage(msg)
		}
	}
}

// raMetrics produces metrics for a given router advertisement.
func (m *Monitor) raMetrics(ra *ndp.RouterAdvertisement, router netaddr.IP) {
	if ra.RouterLifetime == 0 {
		// Not a default router, do nothing.
		return
	}

	// This is an advertisement from a default router.

	// Calculate the UNIX timestamp of when the default route will expire.
	m.mm.MonitorDefaultRouteExpirationTime(
		float64(m.now().Add(ra.RouterLifetime).Unix()),
		m.iface, router.String(),
	)
}

// logf prints a formatted log with the Monitor's interface name.
func (m *Monitor) logf(format string, v ...interface{}) {
	m.ll.Println(m.iface + ": " + fmt.Sprintf(format, v...))
}
