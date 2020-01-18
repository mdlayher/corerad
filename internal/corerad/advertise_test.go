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
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
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
}

type clientContext struct {
	c              conn
	rs             *ndp.RouterSolicitation
	router, client *net.Interface
	reinitC        <-chan struct{}
}

func TestAdvertiserUnsolicitedFull(t *testing.T) {
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
						Prefix:            mustCIDR("2001:db8::/32"),
						OnLink:            true,
						PreferredLifetime: 10 * time.Second,
						ValidLifetime:     20 * time.Second,
					},
					&plugin.RDNSS{
						Lifetime: 10 * time.Second,
						Servers: []net.IP{
							mustIP("2001:db8::1"),
							mustIP("2001:db8::2"),
						},
					},
					plugin.NewMTU(1500),
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
				OtherConfiguration: true,
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
						Prefix:            mustIP("2001:db8::"),
					},
					&ndp.RecursiveDNSServer{
						Lifetime: 10 * time.Second,
						Servers: []net.IP{
							mustIP("2001:db8::1"),
							mustIP("2001:db8::2"),
						},
					},
					ndp.NewMTU(1500),
				},
			}

			// Verify the final option is a NDP SLL of the router, but don't
			// compare it because we don't want to bother comparing against
			// a specific MAC address (which is randomized by the kernel).
			final := ra.Options[len(ra.Options)-1]
			if lla, ok := final.(*ndp.LinkLayerAddress); !ok || lla.Direction != ndp.Source {
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

					// Don't care about options, nothing special is configured
					// for options in the interface config.
					ra := m.(*ndp.RouterAdvertisement)
					ra.Options = nil

					got = append(got, ra)
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

					// Don't care about options, nothing special is configured
					// for options in the interface config.
					ra := m.(*ndp.RouterAdvertisement)
					ra.Options = nil

					got = append(got, ra)
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
			delay: 4 * time.Second,
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
			delay: 52 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := multicastDelay(r, tt.i, tt.min.Nanoseconds(), tt.max.Nanoseconds())
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

	ad := NewAdvertiser(
		cfg.Name,
		*cfg,
		log.New(os.Stderr, "", 0),
		NewAdvertiserMetrics(nil),
	)

	// Swap out the underlying connections for a UDP socket pair.
	sc, cc, cDone := testConnPair(t)
	ad.c = sc

	var eg errgroup.Group
	eg.Go(func() error {
		// TODO: hook into watcher.
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
		reinitC: ad.reinitC,
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

func testConnPair(t *testing.T) (conn, conn, func()) {
	// TODO: parameterize?
	ifi, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skipf("skipping, failed to get loopback: %v", err)
	}

	cc, err := net.ListenPacket("udp6", ":0")
	if err != nil {
		t.Fatalf("failed to create client peer: %v", err)
	}

	// Set up a simulated client/server pair.

	peerC := make(chan struct{})
	server := &udpConn{
		Forwarding:     true,
		ControlMessage: &ipv6.ControlMessage{HopLimit: ndp.HopLimit},

		iface: ifi.Name,
		peer:  cc.LocalAddr(),
		peerC: peerC,
	}

	client := &udpConn{
		ControlMessage: &ipv6.ControlMessage{HopLimit: ndp.HopLimit},

		peerC: peerC,
		pc:    cc,
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()

		// When a message is received, reconfigure the peers for each of these
		// simulated connections, so they continue to point to each other.
		for range peerC {
			server.mu.Lock()
			client.mu.Lock()

			server.peer = client.pc.LocalAddr()
			client.peer = server.pc.LocalAddr()

			client.mu.Unlock()
			server.mu.Unlock()
		}
	}()

	done := func() {
		close(peerC)
		wg.Wait()
	}

	return server, client, done
}

type udpConn struct {
	ControlMessage *ipv6.ControlMessage
	Forwarding     bool

	mu    sync.Mutex
	iface string
	peerC chan struct{}
	peer  net.Addr
	pc    net.PacketConn
}

func (c *udpConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.pc.Close()
}

func (c *udpConn) Dial(_ string) (*net.Interface, net.IP, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ifi, err := net.InterfaceByName(c.iface)
	if err != nil {
		return nil, nil, err
	}

	// Loopback has no address.
	ifi.HardwareAddr = net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad}

	pc, err := net.ListenPacket("udp6", ":0")
	if err != nil {
		return nil, nil, err
	}

	// New connection, update peer associations.
	c.pc = pc
	c.peerC <- struct{}{}

	return ifi, net.IPv6loopback, nil
}

func (c *udpConn) IPv6Forwarding() (bool, error) { return c.Forwarding, nil }

func (c *udpConn) ReadFrom() (ndp.Message, *ipv6.ControlMessage, net.IP, error) {
	b := make([]byte, 1024)
	n, addr, err := c.pc.ReadFrom(b)
	if err != nil {
		return nil, nil, nil, err
	}

	// Got a message, update peer associations.
	c.peerC <- struct{}{}

	m, err := ndp.ParseMessage(b[:n])
	if err != nil {
		return nil, nil, nil, err
	}

	return m, c.ControlMessage, addr.(*net.UDPAddr).IP, nil
}

func (c *udpConn) SetReadDeadline(t time.Time) error { return c.pc.SetReadDeadline(t) }

func (c *udpConn) WriteTo(m ndp.Message, _ *ipv6.ControlMessage, _ net.IP) error {
	c.mu.Lock()
	defer c.mu.Unlock()

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

	ad := NewAdvertiser(router.Name, *cfg, log.New(os.Stderr, "", 0), nil)

	sc := newSystemConn(nil, nil)
	client, _, err := sc.Dial(veth1)
	if err != nil {
		t.Fatalf("failed to dial client connection: %v", err)
	}

	// Only accept RAs.
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterAdvertisement)

	if err := sc.c.SetICMPFilter(&f); err != nil {
		t.Fatalf("failed to apply ICMPv6 filter: %v", err)
	}

	// Enable inspection of IPv6 control messages.
	flags := ipv6.FlagHopLimit | ipv6.FlagDst
	if err := sc.c.SetControlMessage(flags, true); err != nil {
		t.Fatalf("failed to apply IPv6 control message flags: %v", err)
	}

	if err := sc.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("failed to set client read deadline: %v", err)
	}

	cctx := &clientContext{
		c: sc,
		rs: &ndp.RouterSolicitation{
			Options: []ndp.Option{&ndp.LinkLayerAddress{
				Direction: ndp.Source,
				Addr:      client.HardwareAddr,
			}},
		},
		router:  router,
		client:  client,
		reinitC: ad.reinitC,
	}

	done := func() {
		if err := sc.Close(); err != nil {
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

	watchC := make(chan struct{})
	w := NewWatcher(nil)
	w.Register(cctx.router.Name, watchC)

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx, watchC); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	eg.Go(func() error {
		if err := w.Watch(ctx); err != nil {
			return fmt.Errorf("failed to watch: %v", err)
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

	file := sysctl(iface, key)

	t.Logf("sysctl %q = %q", file, value)

	if err := ioutil.WriteFile(file, []byte(value), 0o644); err != nil {
		t.Fatalf("failed to write sysctl %s/%s: %v", iface, key, err)
	}
}

func mustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panicf("failed to parse %q as IP address", s)
	}

	return ip
}
