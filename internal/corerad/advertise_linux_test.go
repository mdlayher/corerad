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

//+build linux

package corerad

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"os/exec"
	"runtime"
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

func TestAdvertiserLinuxUnsolicited(t *testing.T) {
	// Configure a variety of plugins to ensure that everything is handled
	// appropriately over the wire.
	cfg := &config.Interface{
		OtherConfig: true,
		MTU:         1500,
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
		},
	}

	var ra ndp.Message
	ad, done := testAdvertiserClient(t, cfg, nil, func(_ func(), cctx *clientContext) {
		// Read a single advertisement and then ensure the advertiser can be halted.
		m, _, _, err := cctx.c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}
		ra = m
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
			&ndp.LinkLayerAddress{
				Direction: ndp.Source,
				Addr:      ad.mac,
			},
		},
	}

	if diff := cmp.Diff(want, ra); diff != "" {
		t.Fatalf("unexpected router advertisement (-want +got):\n%s", diff)
	}
}

func TestAdvertiserLinuxUnsolicitedShutdown(t *testing.T) {
	// The advertiser will act as a default router until it shuts down.
	const lifetime = 3 * time.Second
	cfg := &config.Interface{
		DefaultLifetime: lifetime,
	}

	var got []ndp.Message
	ad, done := testAdvertiserClient(t, cfg, nil, func(cancel func(), cctx *clientContext) {
		// Read the RA the advertiser sends on startup, then stop it and capture the
		// one it sends on shutdown.
		for i := 0; i < 2; i++ {
			m, _, _, err := cctx.c.ReadFrom()
			if err != nil {
				t.Fatalf("failed to read RA: %v", err)
			}

			got = append(got, m)
			cancel()
		}
	})
	defer done()

	options := []ndp.Option{&ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      ad.mac,
	}}

	// Expect only the first message to contain a RouterLifetime field as it
	// should be cleared on shutdown.
	want := []ndp.Message{
		&ndp.RouterAdvertisement{
			RouterLifetime: lifetime,
			Options:        options,
		},
		&ndp.RouterAdvertisement{
			RouterLifetime: 0,
			Options:        options,
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unexpected router advertisements (-want +got):\n%s", diff)
	}
}

func skipShort(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping in short mode")
	}
}

func TestAdvertiserLinuxUnsolicitedDelayed(t *testing.T) {
	skipShort(t)

	// Configure a variety of plugins to ensure that everything is handled
	// appropriately over the wire.
	var got []ndp.Message
	ad, done := testAdvertiserClient(t, nil, nil, func(_ func(), cctx *clientContext) {
		// Expect a significant delay between the multicast RAs.
		start := time.Now()
		for i := 0; i < 2; i++ {
			m, _, _, err := cctx.c.ReadFrom()
			if err != nil {
				t.Fatalf("failed to read RA: %v", err)
			}
			got = append(got, m)
		}

		if d := time.Since(start); d < minDelayBetweenRAs {
			t.Fatalf("delay too short between multicast RAs: %s", d)
		}
	})
	defer done()

	// Expect identical RAs.
	ra := &ndp.RouterAdvertisement{
		Options: []ndp.Option{
			&ndp.LinkLayerAddress{
				Direction: ndp.Source,
				Addr:      ad.mac,
			},
		},
	}

	if diff := cmp.Diff([]ndp.Message{ra, ra}, got); diff != "" {
		t.Fatalf("unexpected router advertisements (-want +got):\n%s", diff)
	}
}

func TestAdvertiserLinuxSolicited(t *testing.T) {
	// No configuration, bare minimum router advertisement.
	var got []ndp.Message
	ad, done := testAdvertiserClient(t, nil, nil, func(cancel func(), cctx *clientContext) {
		// Issue repeated router solicitations and expect router advertisements
		// in response.
		for i := 0; i < 3; i++ {
			if err := cctx.c.WriteTo(cctx.rs, nil, net.IPv6linklocalallrouters); err != nil {
				t.Fatalf("failed to send RS: %v", err)
			}

			// Read a single advertisement and then ensure the advertiser can be halted.
			m, _, _, err := cctx.c.ReadFrom()
			if err != nil {
				t.Fatalf("failed to read RA: %v", err)
			}

			got = append(got, m)
		}
	})
	defer done()

	ra := &ndp.RouterAdvertisement{
		Options: []ndp.Option{&ndp.LinkLayerAddress{
			Direction: ndp.Source,
			Addr:      ad.mac,
		}},
	}

	want := []ndp.Message{
		ra, ra, ra,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unexpected router advertisement (-want +got):\n%s", diff)
	}
}

func TestAdvertiserLinuxSolicitedBadHopLimit(t *testing.T) {
	_, done := testAdvertiserClient(t, nil, nil, func(cancel func(), cctx *clientContext) {
		// Consume the initial multicast.
		if _, _, _, err := cctx.c.ReadFrom(); err != nil {
			t.Fatalf("failed to read multicast RA: %v", err)
		}

		// Expect a timeout due to bad hop limit.
		if err := cctx.c.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
			t.Fatalf("failed to set client deadline: %v", err)
		}

		cm := &ipv6.ControlMessage{HopLimit: ndp.HopLimit - 1}
		if err := cctx.c.WriteTo(cctx.rs, cm, net.IPv6linklocalallrouters); err != nil {
			t.Fatalf("failed to send RS: %v", err)
		}

		_, _, _, err := cctx.c.ReadFrom()
		if nerr, ok := err.(net.Error); !ok || !nerr.Timeout() {
			t.Fatalf("expected timeout error, but got: %#v", err)
		}
	})
	defer done()
}

func TestAdvertiserLinuxContextCanceled(t *testing.T) {
	ad, _, done := testAdvertiser(t, nil, nil)
	defer done()

	timer := time.AfterFunc(5*time.Second, func() {
		panic("took too long")
	})
	defer timer.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// This should not block because the context is already canceled.
	if err := ad.Advertise(ctx); err != nil {
		t.Fatalf("failed to advertise: %v", err)
	}
}

func TestAdvertiserLinuxIPv6Autoconfiguration(t *testing.T) {
	ad, _, done := testAdvertiser(t, nil, nil)
	defer done()

	// Capture the IPv6 autoconfiguration state while the advertiser is running
	// and immediately after it stops.
	start, err := getIPv6Autoconf(ad.iface)
	if err != nil {
		t.Fatalf("failed to get start state: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group
	eg.Go(func() error {
		// TODO: hook into internal state?
		if err := ad.Advertise(ctx); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	cancel()
	if err := eg.Wait(); err != nil {
		t.Fatalf("failed to stop advertiser: %v", err)
	}

	end, err := getIPv6Autoconf(ad.iface)
	if err != nil {
		t.Fatalf("failed to get end state: %v", err)
	}

	// Expect the advertiser to disable IPv6 autoconfiguration and re-enable
	// it once it's done.
	if diff := cmp.Diff([]bool{true, true}, []bool{start, end}); diff != "" {
		t.Fatalf("unexpected IPv6 autoconfiguration states (-want +got):\n%s", diff)
	}
}

func TestAdvertiserLinuxIPv6Forwarding(t *testing.T) {
	const lifetime = 3 * time.Second
	cfg := &config.Interface{
		DefaultLifetime: lifetime,
	}

	var got []ndp.Message
	ad, done := testAdvertiserClient(t, cfg, nil, func(cancel func(), cctx *clientContext) {
		m0, _, _, err := cctx.c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}

		// Forwarding is disabled after the first RA arrives.
		if err := setIPv6Forwarding(cctx.router.Name, false); err != nil {
			t.Fatalf("failed to disable IPv6 forwarding: %v", err)
		}

		if err := cctx.c.WriteTo(cctx.rs, nil, net.IPv6linklocalallrouters); err != nil {
			t.Fatalf("failed to send RS: %v", err)
		}

		m1, _, _, err := cctx.c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}

		got = []ndp.Message{m0, m1}
	})
	defer done()

	options := []ndp.Option{&ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      ad.mac,
	}}

	// Expect only the first message to contain a RouterLifetime field as it
	// should be cleared on shutdown.
	want := []ndp.Message{
		// Unsolicited.
		&ndp.RouterAdvertisement{
			RouterLifetime: lifetime,
			Options:        options,
		},
		// Solicited.
		&ndp.RouterAdvertisement{
			RouterLifetime: 0,
			Options:        options,
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unexpected router advertisements (-want +got):\n%s", diff)
	}
}

func TestAdvertiserLinuxSLAAC(t *testing.T) {
	// No configuration, bare minimum router advertisement.
	tcfg := &testConfig{
		vethConfig: func(t *testing.T, _, veth1 string) {
			// Ensure SLAAC can be used on the client interface.
			mustSysctl(t, veth1, "autoconf", "1")
		},
	}

	prefix := mustCIDR("2001:db8:dead:beef::/64")

	pfx := plugin.NewPrefix()
	pfx.Prefix = prefix

	icfg := &config.Interface{
		Plugins: []plugin.Plugin{pfx},
	}

	_, done := testAdvertiserClient(t, icfg, tcfg, func(cancel func(), cctx *clientContext) {
		// Consume the initial multicast router advertisement.
		if _, _, _, err := cctx.c.ReadFrom(); err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}

		// And verify that SLAAC addresses were added to the interface.
		addrs, err := cctx.client.Addrs()
		if err != nil {
			t.Fatalf("failed to get interface addresses: %v", err)
		}

		for _, a := range addrs {
			// Skip non IP and link-local addresses.
			a, ok := a.(*net.IPNet)
			if !ok || a.IP.IsLinkLocalUnicast() {
				continue
			}

			// Verify all addresses reside within prefix.
			if !prefix.Contains(a.IP) {
				t.Fatalf("prefix %s does not contain address %s", prefix, a.IP)
			}

			t.Logf("IP: %s", a)
		}
	})
	defer done()
}

func TestAdvertiserLinuxReinitialize(t *testing.T) {
	skipShort(t)

	_, done := testAdvertiserClient(t, nil, nil, func(cancel func(), cctx *clientContext) {
		if err := cctx.c.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			t.Fatalf("failed to extend read deadline: %v", err)
		}

		// Consume the initial multicast router advertisement.
		if _, _, _, err := cctx.c.ReadFrom(); err != nil {
			t.Fatalf("failed to read first RA: %v", err)
		}

		// Now bring the link down so it is not ready, forcing a reinitialization.
		shell(t, "ip", "link", "set", "down", cctx.router.Name)

		// TODO: shorten timeout once link state is watched.
		time.AfterFunc(20*time.Second, func() {
			panic("took too long to reinitialize")
		})

		// Wait for the reinit process to begin, bring the link up, and wait
		// for it to end.
		<-cctx.reinitC
		shell(t, "ip", "link", "set", "up", cctx.router.Name)
		<-cctx.reinitC

		// Consume the multicast router advertisement immediately after reinit.
		m, _, _, err := cctx.c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read second RA: %v", err)
		}

		if _, ok := m.(*ndp.RouterAdvertisement); !ok {
			t.Fatalf("expected router advertisement, but got: %#v", m)
		}
	})
	defer done()
}

type testConfig struct {
	// An optional hook which can be used to apply additional configuration to
	// the test veth interfaces before they are brought up.
	vethConfig func(t *testing.T, veth0, veth1 string)
}

func testAdvertiser(t *testing.T, cfg *config.Interface, tcfg *testConfig) (*Advertiser, *clientContext, func()) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("skipping, advertiser tests only run on Linux")
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

	ad := NewAdvertiser(router.Name, *cfg, nil, nil)

	client, err := net.InterfaceByName(veth1)
	if err != nil {
		t.Fatalf("failed to look up client veth: %v", err)
	}

	c, _, err := ndp.Dial(client, ndp.LinkLocal)
	if err != nil {
		t.Fatalf("failed to create NDP client connection: %v", err)
	}

	// Only accept RAs.
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterAdvertisement)

	if err := c.SetICMPFilter(&f); err != nil {
		t.Fatalf("failed to apply ICMPv6 filter: %v", err)
	}

	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("failed to set client read deadline: %v", err)
	}

	cctx := &clientContext{
		c: c,
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
		if err := c.Close(); err != nil {
			t.Fatalf("failed to close NDP router solicitation connection: %v", err)
		}

		// Clean up the veth pair.
		shell(t, "ip", "link", "del", veth0)
	}

	return ad, cctx, done
}

type clientContext struct {
	c              *ndp.Conn
	rs             *ndp.RouterSolicitation
	router, client *net.Interface
	reinitC        <-chan struct{}
}

// testAdvertiserClient is a wrapper around testAdvertiser which focuses on
// client interactions rather than server interactions.
func testAdvertiserClient(
	t *testing.T,
	cfg *config.Interface,
	tcfg *testConfig,
	fn func(cancel func(), cctx *clientContext),
) (*Advertiser, func()) {
	t.Helper()

	ad, cctx, adDone := testAdvertiser(t, cfg, tcfg)

	ctx, cancel := context.WithCancel(context.Background())

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx); err != nil {
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

	return ad, done
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

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panicf("failed to parse %q as IP address", s)
	}

	return ip
}

func mustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}
