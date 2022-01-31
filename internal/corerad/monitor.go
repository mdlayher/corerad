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

package corerad

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mdlayher/corerad/internal/netstate"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/ndp"
	"golang.org/x/sync/errgroup"
)

// A Monitor listens and reports on NDP traffic.
type Monitor struct {
	// OnMessage is an optional callback which will fire when the monitor
	// receives an NDP message.
	OnMessage func(m ndp.Message)

	// Static configuration.
	cctx    *Context
	iface   string
	verbose bool

	// Socket creation and system state manipulation.
	dialer *system.Dialer
	watchC <-chan netstate.Change

	// Readiness notification.
	readyOnce sync.Once
	readyC    chan struct{}

	// now allows overriding the current time.
	now func() time.Time
}

// NewMonitor creates a Monitor for the specified interface. If ll is nil, logs
// are discarded. If mm is nil, metrics are discarded.
func NewMonitor(
	cctx *Context,
	iface string,
	dialer *system.Dialer,
	watchC <-chan netstate.Change,
	verbose bool,
) *Monitor {
	return &Monitor{
		cctx:    cctx,
		iface:   iface,
		verbose: verbose,
		dialer:  dialer,
		watchC:  watchC,
		readyC:  make(chan struct{}),

		// By default use real time.
		now: time.Now,
	}
}

// Run initializes the configured interface and listening and reporting on
// incoming NDP traffic. Run will block until ctx is canceled or an error
// occurs.
func (m *Monitor) Run(ctx context.Context) error {
	return m.dialer.Dial(ctx, func(ctx context.Context, dctx *system.DialContext) error {
		// Note readiness on first successful init.
		m.readyOnce.Do(func() { close(m.readyC) })
		m.logf("initialized, monitoring from %s", dctx.IP)

		// Monitor until an error occurs, reinitializing under certain
		// circumstances.
		err := m.monitor(ctx, dctx.Conn)
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

// Ready implements Task.
func (m *Monitor) Ready() <-chan struct{} { return m.readyC }

// String implements Task.
func (m *Monitor) String() string { return fmt.Sprintf("monitor %q", m.iface) }

// monitor is the internal loop for Monitor which coordinates the various
// Monitor goroutines.
func (m *Monitor) monitor(ctx context.Context, conn system.Conn) error {
	// Attach the context to the errgroup so that goroutines are canceled when
	// one of them returns an error.
	eg, ctx := errgroup.WithContext(ctx)

	// Listener which listens for and reports on NDP traffic.
	eg.Go(func() error {
		l := newListener(m.cctx, m.iface, conn)
		return l.Listen(ctx, func(msg message) error {
			m.handle(msg.Message, msg.Host.String())

			// Callback must fire after handle to ensure logs and metrics are
			// consistent in tests.
			if m.OnMessage != nil {
				m.OnMessage(msg.Message)
			}

			return nil
		})
	})

	eg.Go(linkStateWatcher(ctx, m.watchC))

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to run Monitor: %w", err)
	}

	// Should only reach this state when context is canceled on shutdown.
	return ctx.Err()
}

// handle handles an incoming NDP message and reports on it.
func (m *Monitor) handle(msg ndp.Message, host string) {
	m.debugf("monitor received %q from %s", msg.Type(), host)

	m.cctx.mm.MonMessagesReceivedTotal(1.0, m.iface, host, msg.Type().String())

	// TODO(mdlayher): expand type switch.
	switch msg := msg.(type) {
	case *ndp.RouterAdvertisement:
		now := m.now()

		if msg.RouterLifetime != 0 {
			// This is an advertisement from a default router. Calculate the
			// UNIX timestamp of when the default route will expire.
			m.cctx.mm.MonDefaultRouteExpirationTime(
				float64(now.Add(msg.RouterLifetime).Unix()),
				m.iface, host,
			)
		}

		// Export metrics for each prefix option.
		for _, p := range pickPrefixes(msg.Options) {
			str := cidrStr(p.Prefix, p.PrefixLength)

			m.cctx.mm.MonPrefixAutonomous(
				boolFloat(p.AutonomousAddressConfiguration),
				m.iface, str, host,
			)

			m.cctx.mm.MonPrefixOnLink(
				boolFloat(p.OnLink),
				m.iface, str, host,
			)

			m.cctx.mm.MonPrefixPreferredLifetimeExpirationTime(
				float64(now.Add(p.PreferredLifetime).Unix()),
				m.iface, str, host,
			)

			m.cctx.mm.MonPrefixValidLifetimeExpirationTime(
				float64(now.Add(p.ValidLifetime).Unix()),
				m.iface, str, host,
			)
		}
	}
}

// logf prints a formatted log with the Monitor's interface name.
func (m *Monitor) logf(format string, v ...interface{}) {
	m.cctx.ll.Printf(m.iface+": "+format, v...)
}

// debugf prints a formatted debug log if verbose mode is configured.
func (m *Monitor) debugf(format string, v ...interface{}) {
	if !m.verbose {
		return
	}

	m.logf("debug: %s", fmt.Sprintf(format, v...))
}
