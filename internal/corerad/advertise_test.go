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
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/crtest"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/metricslite"
	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
	"inet.af/netaddr"
)

// A testAdvertiserFunc is a function which sets up an Advertiser for testing.
type testAdvertiserFunc func(
	t *testing.T,
	cfg *config.Interface,
	tcfg *testConfig,
	fn func(cancel func(), cctx *clientContext),
) func()

type testConfig struct {
	// An optional hook which can be used to apply additional configuration to
	// the test veth interfaces before they are brought up.
	vethConfig func(t *testing.T, veth0, veth1 string)

	// An optional hook for Advertiser.OnInconsistentRA.
	onInconsistentRA func(ours, theirs *ndp.RouterAdvertisement)
}

type clientContext struct {
	c              system.Conn
	rs             *ndp.RouterSolicitation
	router, client *net.Interface
	mm             *Metrics
}

func TestAdvertiserUnsolicitedFull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   testAdvertiserFunc
	}{
		{
			name: "simulated",
			fn:   testSimulatedAdvertiserClient,
		},
		{
			name: "real",
			fn:   testAdvertiserClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Configure a variety of plugins to ensure that everything is handled
			// appropriately over the wire.
			cfg := &config.Interface{
				OtherConfig: true,
				Preference:  ndp.High,
				Plugins: []plugin.Plugin{
					&plugin.DNSSL{
						Lifetime: 10 * time.Second,
						DomainNames: []string{
							"foo.example.com",
							// Unicode was troublesome in package ndp for a while;
							// verify it works here too.
							"🔥.example.com",
						},
					},
					&plugin.Prefix{
						Prefix:            crtest.MustIPPrefix("2001:db8::/32"),
						OnLink:            true,
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
					},
					&plugin.Route{
						Prefix:     crtest.MustIPPrefix("2001:db8:ffff::/64"),
						Preference: ndp.High,
						Lifetime:   10 * time.Second,
					},
					&plugin.RDNSS{
						Lifetime: 10 * time.Second,
						Servers: []netaddr.IP{
							crtest.MustIP("2001:db8::1"),
							crtest.MustIP("2001:db8::2"),
						},
					},
					plugin.NewMTU(1500),
					// Initialized by Plugin.Prepare.
					&plugin.LLA{},
				},
			}

			var ra *ndp.RouterAdvertisement
			done := tt.fn(t, cfg, nil, func(_ func(), cctx *clientContext) {
				// Read a single advertisement and then ensure the advertiser can be halted.
				m, _, _, err := cctx.c.ReadFrom()
				if err != nil {
					t.Fatalf("failed to read RA: %v", err)
				}
				ra = m.(*ndp.RouterAdvertisement)
			})
			defer done()

			// Expect a complete RA.
			want := &ndp.RouterAdvertisement{
				OtherConfiguration:        true,
				RouterSelectionPreference: ndp.High,
				Options: []ndp.Option{
					&ndp.DNSSearchList{
						Lifetime: 10 * time.Second,
						DomainNames: []string{
							"foo.example.com",
							"🔥.example.com",
						},
					},
					&ndp.PrefixInformation{
						PrefixLength:      32,
						OnLink:            true,
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
						Prefix:            mustNetIP("2001:db8::"),
					},
					&ndp.RouteInformation{
						PrefixLength:  64,
						Preference:    ndp.High,
						RouteLifetime: 10 * time.Second,
						Prefix:        mustNetIP("2001:db8:ffff::"),
					},
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers: []net.IP{
							mustNetIP("2001:db8::1"),
							mustNetIP("2001:db8::2"),
						},
					},
					ndp.NewMTU(1500),
				},
			}

			// Verify the final option is a NDP SLL of the router, but don't
			// compare it because we don't want to bother comparing against
			// a specific MAC address (which is randomized by the kernel).
			final := ra.Options[len(ra.Options)-1]
			if lla, ok := final.(*ndp.LinkLayerAddress); !ok || lla.Direction != ndp.Source || len(lla.Addr) != 6 {
				t.Fatalf("final RA option is not source link-layer address option: %#v", final)
			}

			// Option verified, trim it away.
			ra.Options = ra.Options[:len(ra.Options)-1]

			if diff := cmp.Diff(want, ra); diff != "" {
				t.Fatalf("unexpected router advertisement (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAdvertiserUnsolicitedShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   testAdvertiserFunc
	}{
		{
			name: "simulated",
			fn:   testSimulatedAdvertiserClient,
		},
		{
			name: "real",
			fn:   testAdvertiserClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The advertiser will act as a default router until it shuts down.
			const lifetime = 3 * time.Second
			cfg := &config.Interface{
				DefaultLifetime: lifetime,
			}

			done := tt.fn(t, cfg, nil, func(cancel func(), cctx *clientContext) {
				// Read the RA the advertiser sends on startup, then stop it and capture the
				// one it sends on shutdown.
				var got []*ndp.RouterAdvertisement
				for i := 0; i < 2; i++ {
					m, _, _, err := cctx.c.ReadFrom()
					if err != nil {
						t.Fatalf("failed to read RA: %v", err)
					}

					got = append(got, m.(*ndp.RouterAdvertisement))
					cancel()
				}

				// Expect only the first message to contain a RouterLifetime field as it
				// should be cleared on shutdown.
				want := []*ndp.RouterAdvertisement{
					{RouterLifetime: lifetime},
					{RouterLifetime: 0},
				}

				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("unexpected router advertisements (-want +got):\n%s", diff)
				}
			})
			defer done()
		})
	}
}

func TestAdvertiserUnsolicitedDelay(t *testing.T) {
	skipShort(t)
	t.Parallel()

	tests := []struct {
		name string
		fn   testAdvertiserFunc
	}{
		{
			name: "simulated",
			fn:   testSimulatedAdvertiserClient,
		},
		{
			name: "real",
			fn:   testAdvertiserClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := tt.fn(t, nil, nil, func(_ func(), cctx *clientContext) {
				// Expect a significant delay between the multicast RAs.
				start := time.Now()
				for i := 0; i < 2; i++ {
					if _, _, _, err := cctx.c.ReadFrom(); err != nil {
						t.Fatalf("failed to read RA: %v", err)
					}
				}

				if d := time.Since(start); d < minDelayBetweenRAs {
					t.Fatalf("delay too short between multicast RAs: %s", d)
				}
			})
			defer done()
		})
	}
}

func TestAdvertiserSolicited(t *testing.T) {
	skipShort(t)
	t.Parallel()

	tests := []struct {
		name string
		fn   testAdvertiserFunc
	}{
		{
			name: "simulated",
			fn:   testSimulatedAdvertiserClient,
		},
		{
			name: "real",
			fn:   testAdvertiserClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No configuration, bare minimum router advertisement.
			done := tt.fn(t, nil, nil, func(cancel func(), cctx *clientContext) {
				if _, _, _, err := cctx.c.ReadFrom(); err != nil {
					t.Fatalf("failed to read initial RA: %v", err)
				}

				// Issue repeated router solicitations and expect router advertisements
				// in response.
				var got []*ndp.RouterAdvertisement
				for i := 0; i < 3; i++ {
					if err := cctx.c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
						t.Fatalf("failed to extend read deadline: %v", err)
					}

					if err := cctx.c.WriteTo(cctx.rs, nil, net.IPv6linklocalallrouters); err != nil {
						t.Fatalf("failed to send RS: %v", err)
					}

					m, _, _, err := cctx.c.ReadFrom()
					if err != nil {
						t.Fatalf("failed to read RA: %v", err)
					}

					got = append(got, m.(*ndp.RouterAdvertisement))
				}

				// "Default" RAs.
				want := []*ndp.RouterAdvertisement{{}, {}, {}}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("unexpected router advertisement (-want +got):\n%s", diff)
				}
			})
			defer done()
		})
	}
}

func TestAdvertiserVerifyRAs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   testAdvertiserFunc
	}{
		{
			name: "simulated",
			fn:   testSimulatedAdvertiserClient,
		},
		{
			name: "real",
			fn:   testAdvertiserClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Whenever an inconsistent RA is detected, fire the hook and
			// consume it on a channel.
			raC := make(chan *ndp.RouterAdvertisement)
			tcfg := &testConfig{
				onInconsistentRA: func(_, theirs *ndp.RouterAdvertisement) {
					raC <- theirs
				},
			}

			done := tt.fn(t, nil, tcfg, func(_ func(), cctx *clientContext) {
				// Capture the advertiser's initial RA so that we can manipulate
				// and reflect it to test RA verification.
				m, _, ip, err := cctx.c.ReadFrom()
				if err != nil {
					t.Fatalf("failed to read initial RA: %v", err)
				}
				ra := m.(*ndp.RouterAdvertisement)

				// Copy over our source link-layer address from the synthetic RS
				// for reporting, and make a copy for later comparisons.
				ra.Options = cctx.rs.Options

				want := *ra

				timer := time.AfterFunc(10*time.Second, func() {
					panic("took too long")
				})
				defer timer.Stop()

				// Reflect the router advertisement several times and look for
				// inconsistencies.

				var wg sync.WaitGroup
				wg.Add(1)
				defer wg.Wait()

				go func() {
					defer wg.Done()

					// Reflect the router advertisement 5 times with a minor
					// inconsistency in one of those RAs.
					for i := 0; i < 5; i++ {
						var managed bool
						if i == 3 {
							managed = true
						}

						ra.ManagedConfiguration = managed
						if err := cctx.c.WriteTo(ra, nil, ip); err != nil {
							panicf("failed to send RA: %v", err)
						}
					}
				}()

				// Expect to receive an RA that is identical but has a modified
				// managed flag.
				want.ManagedConfiguration = true
				got := <-raC
				if diff := cmp.Diff(&want, got); diff != "" {
					t.Fatalf("unexpected router advertisement (-want +got):\n%s", diff)
				}

				// Verify that a metric was produced indicating an RA
				// inconsistency detected by this interface.
				ts := findMetric(t, cctx.mm, raInconsistencies)

				label := fmt.Sprintf("interface=%s", cctx.router.Name)
				if diff := cmp.Diff(1., ts.Samples[label]); diff != "" {
					t.Fatalf("unexpected value for interface inconsistencies (-want +got):\n%s", diff)
				}
			})
			defer done()
		})
	}
}

func TestAdvertiserPrometheusMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   testAdvertiserFunc
	}{
		{
			name: "simulated",
			fn:   testSimulatedAdvertiserClient,
		},
		{
			name: "real",
			fn:   testAdvertiserClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Interface{
				Plugins: []plugin.Plugin{
					// Expose two prefixes with differing flags to verify
					// against the metrics output.
					&plugin.Prefix{
						Prefix:            crtest.MustIPPrefix("2001:db8:1111::/64"),
						Autonomous:        true,
						OnLink:            true,
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
					},
					&plugin.Prefix{
						Prefix:            crtest.MustIPPrefix("2001:db8:2222::/64"),
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
					},
				},
			}

			var (
				ra                 *ndp.RouterAdvertisement
				pfxAuto, pfxOnLink metricslite.Series
				iface              string
			)

			// TODO(mdlayher): consider refactoring clientContext so that it
			// is also a return value of each tt.fn invocation.

			done := tt.fn(t, cfg, nil, func(_ func(), cctx *clientContext) {
				m, _, _, err := cctx.c.ReadFrom()
				if err != nil {
					t.Fatalf("failed to read RA: %v", err)
				}

				// Gather only the necessary information after a single RA and
				// immediately stop the Advertiser to verify the output.
				ra = m.(*ndp.RouterAdvertisement)
				pfxAuto = findMetric(t, cctx.mm, raPrefixAutonomous)
				pfxOnLink = findMetric(t, cctx.mm, raPrefixOnLink)
				iface = cctx.router.Name
			})
			done()

			// Verify the presence of matching metrics for any prefixes produced
			// by the router advertisement.
			var (
				auto   = make(map[string]float64)
				onLink = make(map[string]float64)
			)

			for _, p := range pickPrefixes(ra.Options) {
				labels := fmt.Sprintf("interface=%s,prefix=%s/%d",
					iface, p.Prefix, p.PrefixLength)

				auto[labels] = boolFloat(p.AutonomousAddressConfiguration)
				onLink[labels] = boolFloat(p.OnLink)
			}

			if diff := cmp.Diff(pfxAuto.Samples, auto); diff != "" {
				t.Fatalf("unexpected prefix autonomous timeseries (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(pfxOnLink.Samples, auto); diff != "" {
				t.Fatalf("unexpected prefix on-link timeseries (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_multicastDelay(t *testing.T) {
	// Static seed for deterministic output.
	r := rand.New(rand.NewSource(0))

	tests := []struct {
		name            string
		i               int
		min, max, delay time.Duration
	}{
		{
			name:  "static",
			min:   1 * time.Second,
			max:   1 * time.Second,
			delay: 1 * time.Second,
		},
		{
			name:  "random",
			min:   1 * time.Second,
			max:   10 * time.Second,
			delay: 5 * time.Second,
		},
		{
			name: "clamped",
			// Delay too long for low i value.
			i:     1,
			min:   30 * time.Second,
			max:   60 * time.Second,
			delay: maxInitialAdvInterval,
		},
		{
			name: "not clamped",
			// Delay appropriate for high i value.
			i:     100,
			min:   30 * time.Second,
			max:   60 * time.Second,
			delay: 54 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := multicastDelay(r, tt.i, tt.min, tt.max)
			if diff := cmp.Diff(tt.delay, d); diff != "" {
				t.Fatalf("unexpected delay (-want +got):\n%s", diff)
			}
		})
	}
}

func testSimulatedAdvertiserClient(
	t *testing.T,
	cfg *config.Interface,
	tcfg *testConfig,
	fn func(cancel func(), cctx *clientContext),
) func() {
	if cfg == nil {
		cfg = &config.Interface{}
	}

	// Fixed interval for multicast advertisements.
	cfg.MinInterval = 1 * time.Second
	cfg.MaxInterval = 1 * time.Second
	cfg.Name = "test0"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set up metrics node so we can inspect its contents at a later time.
	ts := system.TestState{Forwarding: true}
	mm := NewMetrics(metricslite.NewMemory(), ts, []config.Interface{*cfg})
	ad := NewAdvertiser(
		cfg.Name,
		*cfg,
		log.New(os.Stderr, "", 0),
		mm,
	)

	if tcfg != nil && tcfg.onInconsistentRA != nil {
		ad.OnInconsistentRA = tcfg.onInconsistentRA
	}

	// Swap out the underlying connections for a UDP socket pair.
	sc, cc, cDone := testConnPair(t)
	ad.state = ts

	ad.dialer = &system.Dialer{
		DialFunc: func() *system.DialContext {
			return &system.DialContext{
				Conn: sc,
				Interface: &net.Interface{
					Name:         "test0",
					HardwareAddr: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad},
				},
				IP: net.IPv6loopback,
			}
		},
	}

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx, nil); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	cctx := &clientContext{
		c: cc,
		rs: &ndp.RouterSolicitation{
			Options: []ndp.Option{&ndp.LinkLayerAddress{
				Direction: ndp.Source,
				Addr:      net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad},
			}},
		},
		router: &net.Interface{Name: cfg.Name},
		mm:     mm,
	}

	// Run the advertiser and invoke the client's input function with some
	// context for the test, while also allowing the client to cancel the
	// advertiser run loop.
	fn(cancel, cctx)

	done := func() {
		cancel()
		if err := eg.Wait(); err != nil {
			t.Fatalf("failed to stop advertiser: %v", err)
		}

		cDone()
	}

	return done
}

func testConnPair(t *testing.T) (system.Conn, system.Conn, func()) {
	sc, err := net.ListenPacket("udp6", ":0")
	if err != nil {
		t.Fatalf("failed to create server peer: %v", err)
	}

	cc, err := net.ListenPacket("udp6", ":0")
	if err != nil {
		t.Fatalf("failed to create client peer: %v", err)
	}

	// Set up a simulated client/server pair.

	server := &udpConn{
		ControlMessage: &ipv6.ControlMessage{HopLimit: ndp.HopLimit},
		peer:           cc.LocalAddr(),
		pc:             sc,
	}

	client := &udpConn{
		ControlMessage: &ipv6.ControlMessage{HopLimit: ndp.HopLimit},
		peer:           sc.LocalAddr(),
		pc:             cc,
	}

	return server, client, func() {
		if err := sc.Close(); err != nil {
			t.Fatalf("failed to close server: %v", err)
		}
		if err := cc.Close(); err != nil {
			t.Fatalf("failed to close client: %v", err)
		}
	}
}

type udpConn struct {
	ControlMessage *ipv6.ControlMessage

	peer net.Addr
	pc   net.PacketConn
}

var _ system.Conn = &udpConn{}

func (c *udpConn) Close() error { return c.pc.Close() }

func (c *udpConn) ReadFrom() (ndp.Message, *ipv6.ControlMessage, net.IP, error) {
	b := make([]byte, 1024)
	n, addr, err := c.pc.ReadFrom(b)
	if err != nil {
		return nil, nil, nil, err
	}

	m, err := ndp.ParseMessage(b[:n])
	if err != nil {
		return nil, nil, nil, err
	}

	return m, c.ControlMessage, addr.(*net.UDPAddr).IP, nil
}

func (c *udpConn) SetReadDeadline(t time.Time) error { return c.pc.SetReadDeadline(t) }

func (c *udpConn) WriteTo(m ndp.Message, _ *ipv6.ControlMessage, _ net.IP) error {
	b, err := ndp.MarshalMessage(m)
	if err != nil {
		return err
	}

	_, err = c.pc.WriteTo(b, c.peer)
	return err
}

func testAdvertiser(t *testing.T, cfg *config.Interface, tcfg *testConfig) (*Advertiser, *clientContext, func()) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("skipping, this test only runs on Linux")
	}

	skipUnprivileged(t)

	if tcfg == nil {
		tcfg = &testConfig{}
	}

	var (
		r     = rand.New(rand.NewSource(time.Now().UnixNano()))
		veth0 = fmt.Sprintf("cradveth%d", r.Intn(65535))
		veth1 = fmt.Sprintf("cradveth%d", r.Intn(65535))
	)

	// Set up a temporary veth pair in the appropriate state for use with
	// the tests.
	// TODO: use rtnetlink.
	shell(t, "ip", "link", "add", veth0, "type", "veth", "peer", "name", veth1)
	mustSysctl(t, veth0, "accept_dad", "0")
	mustSysctl(t, veth1, "accept_dad", "0")
	mustSysctl(t, veth0, "forwarding", "1")

	if tcfg.vethConfig != nil {
		tcfg.vethConfig(t, veth0, veth1)
	}

	shell(t, "ip", "link", "set", "up", veth0)
	shell(t, "ip", "link", "set", "up", veth1)

	// Make sure the interfaces are up and ready.
	waitInterfacesReady(t, veth0, veth1)

	// Allow empty config but always populate the interface name.
	// TODO: consider building veth pairs within the tests.
	if cfg == nil {
		cfg = &config.Interface{}
	}
	// Fixed interval for multicast advertisements.
	cfg.MinInterval = 1 * time.Second
	cfg.MaxInterval = 1 * time.Second
	cfg.Name = veth0

	router, err := net.InterfaceByName(veth0)
	if err != nil {
		t.Fatalf("failed to look up router veth: %v", err)
	}

	client, err := net.InterfaceByName(veth1)
	if err != nil {
		t.Fatalf("failed to look up client veth: %v", err)
	}

	// Set up metrics node so we can inspect its contents at a later time.
	mm := NewMetrics(metricslite.NewMemory(), system.NewState(), []config.Interface{*cfg})
	ad := NewAdvertiser(router.Name, *cfg, log.New(os.Stderr, "", 0), mm)

	if tcfg != nil && tcfg.onInconsistentRA != nil {
		ad.OnInconsistentRA = tcfg.onInconsistentRA
	}

	cconn, _, err := ndp.Dial(client, ndp.LinkLocal)
	if err != nil {
		t.Fatalf("failed to dial client connection: %v", err)
	}

	// Only accept RAs.
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterAdvertisement)

	if err := cconn.SetICMPFilter(&f); err != nil {
		t.Fatalf("failed to apply ICMPv6 filter: %v", err)
	}

	// Enable inspection of IPv6 control messages.
	flags := ipv6.FlagHopLimit | ipv6.FlagDst
	if err := cconn.SetControlMessage(flags, true); err != nil {
		t.Fatalf("failed to apply IPv6 control message flags: %v", err)
	}

	if err := cconn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("failed to set client read deadline: %v", err)
	}

	cctx := &clientContext{
		c: cconn,
		rs: &ndp.RouterSolicitation{
			Options: []ndp.Option{&ndp.LinkLayerAddress{
				Direction: ndp.Source,
				Addr:      client.HardwareAddr,
			}},
		},
		router: router,
		client: client,
		mm:     mm,
	}

	done := func() {
		if err := cconn.Close(); err != nil {
			t.Fatalf("failed to close NDP router solicitation connection: %v", err)
		}

		// Clean up the veth pair.
		shell(t, "ip", "link", "del", veth0)
	}

	return ad, cctx, done
}

// testAdvertiserClient is a wrapper around testAdvertiser which focuses on
// client interactions rather than server interactions.
func testAdvertiserClient(
	t *testing.T,
	cfg *config.Interface,
	tcfg *testConfig,
	fn func(cancel func(), cctx *clientContext),
) func() {
	t.Helper()

	ad, cctx, adDone := testAdvertiser(t, cfg, tcfg)

	ctx, cancel := context.WithCancel(context.Background())

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx, nil); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	// Run the advertiser and invoke the client's input function with some
	// context for the test, while also allowing the client to cancel the
	// advertiser run loop.
	fn(cancel, cctx)

	done := func() {
		cancel()
		if err := eg.Wait(); err != nil {
			t.Fatalf("failed to stop advertiser: %v", err)
		}

		adDone()
	}

	return done
}

func waitInterfacesReady(t *testing.T, ifi0, ifi1 string) {
	t.Helper()

	a, err := net.InterfaceByName(ifi0)
	if err != nil {
		t.Fatalf("failed to get first interface: %v", err)
	}

	b, err := net.InterfaceByName(ifi1)
	if err != nil {
		t.Fatalf("failed to get second interface: %v", err)
	}

	for i := 0; i < 5; i++ {
		aaddrs, err := a.Addrs()
		if err != nil {
			t.Fatalf("failed to get first addresses: %v", err)
		}

		baddrs, err := b.Addrs()
		if err != nil {
			t.Fatalf("failed to get second addresses: %v", err)
		}

		if len(aaddrs) > 0 && len(baddrs) > 0 {
			return
		}

		time.Sleep(1 * time.Second)
		t.Log("waiting for interface readiness...")
	}

	t.Fatal("failed to wait for interface readiness")
}

func skipShort(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping in short mode")
	}
}

func skipUnprivileged(t *testing.T) {
	const ifName = "cradprobe0"
	shell(t, "ip", "tuntap", "add", ifName, "mode", "tun")
	shell(t, "ip", "link", "del", ifName)
}

func shell(t *testing.T, name string, arg ...string) {
	t.Helper()

	bin, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("failed to look up binary path: %v", err)
	}

	t.Logf("$ %s %v", bin, arg)

	cmd := exec.Command(bin, arg...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start command %q: %v", name, err)
	}

	if err := cmd.Wait(); err != nil {
		// Shell operations in these tests require elevated privileges.
		if cmd.ProcessState.ExitCode() == int(unix.EPERM) {
			t.Skipf("skipping, permission denied: %v", err)
		}

		t.Fatalf("failed to wait for command %q: %v", name, err)
	}
}

func mustSysctl(t *testing.T, iface, key, value string) {
	t.Helper()

	file := filepath.Join(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s", iface), key)

	t.Logf("sysctl %q = %q", file, value)

	if err := ioutil.WriteFile(file, []byte(value), 0o644); err != nil {
		t.Fatalf("failed to write sysctl %s/%s: %v", iface, key, err)
	}
}

func mustNetIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panicf("failed to parse %q as IP address", s)
	}

	return ip
}

func findMetric(t *testing.T, mm *Metrics, name string) metricslite.Series {
	t.Helper()

	series, ok := mm.Series()
	if !ok {
		t.Fatalf("metrics node does not support Series output: %T", mm)
	}

	for sname, ts := range series {
		// Filter to only return the specified metric.
		if sname != name {
			continue
		}

		return ts
	}

	t.Fatalf("no metric with name %q was found", name)
	panic("unreachable")
}
