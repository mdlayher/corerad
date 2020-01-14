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
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/plugin"
	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
	"golang.org/x/sync/errgroup"
)

func TestAdvertiserLinuxSolicitedBadHopLimit(t *testing.T) {
	done := testAdvertiserClient(t, nil, nil, func(cancel func(), cctx *clientContext) {
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
	if err := ad.Advertise(ctx, nil); err != nil {
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
		if err := ad.Advertise(ctx, nil); err != nil {
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

	done := testAdvertiserClient(t, cfg, nil, func(cancel func(), cctx *clientContext) {
		m0, _, _, err := cctx.c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}

		// Forwarding is disabled after the first RA arrives.
		mustSysctl(t, cctx.router.Name, "forwarding", "0")

		if err := cctx.c.WriteTo(cctx.rs, nil, net.IPv6linklocalallrouters); err != nil {
			t.Fatalf("failed to send RS: %v", err)
		}

		m1, _, _, err := cctx.c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}

		// Expect only the first message to contain a RouterLifetime field as it
		// should be cleared when forwarding is disabled.
		want := []*ndp.RouterAdvertisement{
			{RouterLifetime: lifetime},
			{RouterLifetime: 0},
		}

		// Don't care about options, nothing special is configured for options
		// in the interface config.
		ra0 := m0.(*ndp.RouterAdvertisement)
		ra0.Options = nil
		ra1 := m1.(*ndp.RouterAdvertisement)
		ra1.Options = nil

		got := []*ndp.RouterAdvertisement{ra0, ra1}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("unexpected router advertisements (-want +got):\n%s", diff)
		}
	})
	defer done()
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

	done := testAdvertiserClient(t, icfg, tcfg, func(cancel func(), cctx *clientContext) {
		// Consume the initial multicast router advertisement.
		_, cm, _, err := cctx.c.ReadFrom()
		if err != nil {
			t.Fatalf("failed to read RA: %v", err)
		}

		if !cm.Dst.IsLinkLocalMulticast() {
			t.Fatalf("initial RA address must be multicast: %v", cm.Dst)
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

	done := testAdvertiserClient(t, nil, nil, func(cancel func(), cctx *clientContext) {
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

func TestAdvertiserLinuxSolicitedUnicastOnly(t *testing.T) {
	cfg := &config.Interface{UnicastOnly: true}
	done := testAdvertiserClient(t, cfg, nil, func(cancel func(), cctx *clientContext) {
		// Issue repeated router solicitations and expect router advertisements
		// in response.
		for i := 0; i < 3; i++ {
			if err := cctx.c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatalf("failed to extend read deadline: %v", err)
			}

			if err := cctx.c.WriteTo(cctx.rs, nil, net.IPv6linklocalallrouters); err != nil {
				t.Fatalf("failed to send RS: %v", err)
			}

			_, cm, _, err := cctx.c.ReadFrom()
			if err != nil {
				t.Fatalf("failed to read RA: %v", err)
			}

			if cm.Dst.IsLinkLocalMulticast() {
				t.Fatalf("RA address must not be multicast: %v", cm.Dst)
			}
		}
	})
	defer done()
}
