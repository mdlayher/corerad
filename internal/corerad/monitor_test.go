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
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/metricslite"
	"github.com/mdlayher/ndp"
	"golang.org/x/sync/errgroup"
)

func TestMonitorMetrics(t *testing.T) {
	readyC, onMessage := makeReady()
	cctx := testSimulatedMonitorClient(t, onMessage)

	var (
		sll = &ndp.LinkLayerAddress{
			Direction: ndp.Source,
			Addr:      net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad},
		}

		routerLifetime = 30 * time.Second

		// TODO(mdlayher): expand with other fields.
		rs = &ndp.RouterSolicitation{Options: []ndp.Option{sll}}
		ra = &ndp.RouterAdvertisement{
			// Pretend to be a default router.
			RouterLifetime: routerLifetime,
			Options: []ndp.Option{
				sll,
				&ndp.PrefixInformation{
					Prefix:            netip.MustParseAddr("2001:db8::"),
					PrefixLength:      32,
					OnLink:            true,
					ValidLifetime:     2 * time.Minute,
					PreferredLifetime: 1 * time.Minute,
				},
			},
		}
	)

	// Continue sending messages to the monitor and waiting for it to
	// acknowledge them via receiving on readyC.
	for i := 0; i < 5; i++ {
		// No else allowed!
		var m ndp.Message = ra
		if i%2 == 0 {
			m = rs
		}

		if err := cctx.c.WriteTo(m, nil, system.IPv6LinkLocalAllRouters); err != nil {
			t.Fatalf("failed to send NDP message: %v", err)
		}

		<-readyC
	}

	// Now that we've sent our messages, verify the output of the monitor
	// messages received time series.
	want := []metricslite.Series{
		{
			Name: monReceived,
			Samples: map[string]float64{
				"interface=test0,host=::1,message=router advertisement": 2,
				"interface=test0,host=::1,message=router solicitation":  3,
			},
		},
		{
			Name: monDefaultRoute,
			Samples: map[string]float64{
				// Because the fixed time is UNIX 0, we can use router lifetime in
				// seconds directly as the timestamp for when the default route
				// would actually expire.
				"interface=test0,router=::1": routerLifetime.Seconds(),
			},
		},
		{
			Name: monPrefixAutonomous,
			Samples: map[string]float64{
				"interface=test0,prefix=2001:db8::/32,router=::1": 0,
			},
		},
		{
			Name: monPrefixOnLink,
			Samples: map[string]float64{
				"interface=test0,prefix=2001:db8::/32,router=::1": 1,
			},
		},
		{
			Name: monPrefixPreferred,
			Samples: map[string]float64{
				"interface=test0,prefix=2001:db8::/32,router=::1": 60,
			},
		},
		{
			Name: monPrefixValid,
			Samples: map[string]float64{
				"interface=test0,prefix=2001:db8::/32,router=::1": 120,
			},
		},
	}

	for _, w := range want {
		t.Run(w.Name, func(t *testing.T) {
			got := findMetric(t, cctx.mm, w.Name)
			if diff := cmp.Diff(w, got); diff != "" {
				t.Fatalf("unexpected timeseries (-want +got):\n%s", diff)
			}
		})
	}
}

func fixedNow() time.Time { return time.Unix(0, 0) }

func makeReady() (<-chan struct{}, func(ndp.Message)) {
	readyC := make(chan struct{})
	return readyC, func(m ndp.Message) {
		readyC <- struct{}{}
	}
}

func testSimulatedMonitorClient(t *testing.T, onMessage func(m ndp.Message)) *clientContext {
	// Swap out the underlying connections for a UDP socket pair.
	sc, cc := testConnPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	const iface = "test0"

	// Set up metrics node so we can inspect its contents at a later time.
	state := system.TestState{Forwarding: true}
	mm := NewMetrics(metricslite.NewMemory(), state, nil)

	crctx := NewContext(nil, mm, state)

	mon := NewMonitor(
		crctx,
		iface,
		&system.Dialer{
			DialFunc: func() (*system.DialContext, error) {
				return &system.DialContext{
					Conn: sc,
					Interface: &net.Interface{
						Name:         iface,
						HardwareAddr: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad},
					},
					IP: system.IPv6Loopback,
				}, nil
			},
		},
		nil,
		// Enable verbose logs for better debuggability.
		true,
	)

	mon.OnMessage = onMessage

	// Always use a fixed time.
	mon.now = fixedNow

	readyC := make(chan struct{})

	var eg errgroup.Group
	eg.Go(func() error {
		close(readyC)
		if err := mon.Run(ctx); err != nil {
			return fmt.Errorf("failed to monitor: %v", err)
		}

		return nil
	})

	<-readyC

	t.Cleanup(func() {
		cancel()
		if err := eg.Wait(); err != nil {
			t.Fatalf("failed to stop monitor: %v", err)
		}
	})

	return &clientContext{
		c:      cc,
		router: &net.Interface{Name: iface},
		mm:     mm,
	}
}
