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
	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

func TestAdvertiserLinuxUnsolicited(t *testing.T) {
	// Configure a variety of plugins to ensure that everything is handled
	// appropriately over the wire.
	ad, c, _, done := testAdvertiser(t, &config.Interface{
		OtherConfig: true,
		Plugins: []config.Plugin{
			&config.DNSSL{
				Lifetime: 10 * time.Second,
				DomainNames: []string{
					"foo.example.com",
					// Unicode was troublesome in package ndp for a while;
					// verify it works here too.
					"🔥.example.com",
				},
			},
			newMTU(1500),
			&config.Prefix{
				Prefix:            mustCIDR("2001:db8::/32"),
				OnLink:            true,
				PreferredLifetime: 10 * time.Second,
				ValidLifetime:     20 * time.Second,
			},
			&config.RDNSS{
				Lifetime: 10 * time.Second,
				Servers: []net.IP{
					mustIP("2001:db8::1"),
					mustIP("2001:db8::2"),
				},
			},
		},
	})
	defer done()

	if err := c.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
		t.Fatalf("failed to set client read deadline: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	// Read a single advertisement and then ensure the advertiser can be halted.
	m, _, _, err := c.ReadFrom()
	if err != nil {
		t.Fatalf("failed to read RA: %v", err)
	}

	cancel()
	if err := eg.Wait(); err != nil {
		t.Fatalf("failed to stop advertiser: %v", err)
	}

	ra, ok := m.(*ndp.RouterAdvertisement)
	if !ok {
		t.Fatalf("did not receive an RA: %#v", m)
	}

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
			ndp.NewMTU(1500),
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
			&ndp.LinkLayerAddress{
				Direction: ndp.Source,
				Addr:      ad.ifi.HardwareAddr,
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
	ad, c, _, done := testAdvertiser(t, &config.Interface{
		DefaultLifetime: lifetime,
	})
	defer done()

	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("failed to set client read deadline: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	// Read the RA the advertiser sends on startup, then stop it and capture the
	// one it sends on shutdown.
	var got []ndp.Message
	for i := 0; i < 2; i++ {
		m, _, _, err := c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}

		got = append(got, m)
		cancel()
	}

	if err := eg.Wait(); err != nil {
		t.Fatalf("failed to stop advertiser: %v", err)
	}

	options := []ndp.Option{&ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      ad.ifi.HardwareAddr,
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

func TestAdvertiserLinuxSolicited(t *testing.T) {
	// No configuration, bare minimum router advertisement.
	ad, c, mac, done := testAdvertiser(t, nil)
	defer done()

	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("failed to set client read deadline: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	rs := &ndp.RouterSolicitation{
		Options: []ndp.Option{&ndp.LinkLayerAddress{
			Direction: ndp.Source,
			Addr:      mac,
		}},
	}

	// There was no config specified, so assume the bare minimum for a valid RA.
	want := &ndp.RouterAdvertisement{
		Options: []ndp.Option{&ndp.LinkLayerAddress{
			Direction: ndp.Source,
			Addr:      ad.ifi.HardwareAddr,
		}},
	}

	// Issue repeated router solicitations and expect router advertisements
	// in response.
	for i := 0; i < 5; i++ {
		if err := c.WriteTo(rs, nil, net.IPv6linklocalallrouters); err != nil {
			t.Fatalf("failed to send RS: %v", err)
		}

		// Read a single advertisement and then ensure the advertiser can be halted.
		m, _, _, err := c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}

		ra, ok := m.(*ndp.RouterAdvertisement)
		if !ok {
			t.Fatalf("did not receive an RA: %#v", m)
		}

		if diff := cmp.Diff(want, ra); diff != "" {
			t.Fatalf("unexpected router advertisement (-want +got):\n%s", diff)
		}
	}

	cancel()
	if err := eg.Wait(); err != nil {
		t.Fatalf("failed to stop advertiser: %v", err)
	}
}

func TestAdvertiserLinuxContextCanceled(t *testing.T) {
	ad, _, _, done := testAdvertiser(t, nil)
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
	ad, _, _, done := testAdvertiser(t, nil)
	defer done()

	// Capture the IPv6 autoconfiguration state while the advertiser is running
	// and immediately after it stops.
	start, err := getIPv6Autoconf(ad.ifi.Name)
	if err != nil {
		t.Fatalf("failed to get start state: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	cancel()
	if err := eg.Wait(); err != nil {
		t.Fatalf("failed to stop advertiser: %v", err)
	}

	end, err := getIPv6Autoconf(ad.ifi.Name)
	if err != nil {
		t.Fatalf("failed to get end state: %v", err)
	}

	// Expect the advertiser to disable IPv6 autoconfiguration and re-enable
	// it once it's done.
	if diff := cmp.Diff([]bool{false, true}, []bool{start, end}); diff != "" {
		t.Fatalf("unexpected IPv6 autoconfiguration states (-want +got):\n%s", diff)
	}
}

func TestAdvertiserLinuxIPv6Forwarding(t *testing.T) {
	const lifetime = 3 * time.Second
	ad, c, mac, done := testAdvertiser(t, &config.Interface{
		DefaultLifetime: lifetime,
	})
	defer done()

	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("failed to set client read deadline: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group
	eg.Go(func() error {
		if err := ad.Advertise(ctx); err != nil {
			return fmt.Errorf("failed to advertise: %v", err)
		}

		return nil
	})

	m0, _, _, err := c.ReadFrom()
	if err != nil {
		t.Fatalf("failed to read RA: %v", err)
	}

	// Forwarding is disabled after the first RA arrives.
	if err := setIPv6Forwarding(ad.ifi.Name, false); err != nil {
		t.Fatalf("failed to disable IPv6 forwarding: %v", err)
	}

	rs := &ndp.RouterSolicitation{
		Options: []ndp.Option{&ndp.LinkLayerAddress{
			Direction: ndp.Source,
			Addr:      mac,
		}},
	}

	if err := c.WriteTo(rs, nil, net.IPv6linklocalallrouters); err != nil {
		t.Fatalf("failed to send RS: %v", err)
	}

	m1, _, _, err := c.ReadFrom()
	if err != nil {
		t.Fatalf("failed to read RA: %v", err)
	}

	cancel()
	if err := eg.Wait(); err != nil {
		t.Fatalf("failed to stop advertiser: %v", err)
	}

	options := []ndp.Option{&ndp.LinkLayerAddress{
		Direction: ndp.Source,
		Addr:      ad.ifi.HardwareAddr,
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

	if diff := cmp.Diff(want, []ndp.Message{m0, m1}); diff != "" {
		t.Fatalf("unexpected router advertisements (-want +got):\n%s", diff)
	}
}

func testAdvertiser(t *testing.T, cfg *config.Interface) (*Advertiser, *ndp.Conn, net.HardwareAddr, func()) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("skipping, advertiser tests only run on Linux")
	}

	skipUnprivileged(t)

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
	shell(t, "ip", "link", "set", "up", veth0)
	shell(t, "ip", "link", "set", "up", veth1)

	// Make sure the interfaces are up and ready.
	waitInterfacesReady(t, veth0, veth1)

	// Allow empty config but always populate the interface name.
	// TODO: consider building veth pairs within the tests.
	if cfg == nil {
		cfg = &config.Interface{
			// Fixed interval for multicast advertisements.
			MinInterval: 1 * time.Second,
			MaxInterval: 1 * time.Second,
		}
	}
	cfg.Name = veth0

	ad, err := NewAdvertiser(*cfg, nil)
	if err != nil {
		t.Fatalf("failed to create advertiser: %v", err)
	}

	ifi, err := net.InterfaceByName(veth1)
	if err != nil {
		t.Skipf("skipping, failed to look up second veth: %v", err)
	}

	c, _, err := ndp.Dial(ifi, ndp.LinkLocal)
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

	done := func() {
		if err := c.Close(); err != nil {
			t.Fatalf("failed to close NDP router solicitation connection: %v", err)
		}

		// Clean up the veth pair.
		shell(t, "ip", "link", "del", veth0)
	}

	return ad, c, ifi.HardwareAddr, done
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

	if err := ioutil.WriteFile(sysctl(iface, key), []byte(value), 0o644); err != nil {
		t.Fatalf("failed to write sysctl %s/%s: %v", iface, key, err)
	}
}
