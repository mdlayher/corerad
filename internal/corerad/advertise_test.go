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
	"math/rand"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
	"golang.org/x/sync/errgroup"
)

func TestAdvertiserUnsolicited(t *testing.T) {
	// No configuration, bare minimum router advertisement.
	ad, c, _, done := testAdvertiser(t, nil)
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

	// There was no config specified, so assume the bare minimum for a valid RA.
	want := &ndp.RouterAdvertisement{
		Options: []ndp.Option{&ndp.LinkLayerAddress{
			Direction: ndp.Source,
			Addr:      ad.ifi.HardwareAddr,
		}},
	}

	if diff := cmp.Diff(want, ra); diff != "" {
		t.Fatalf("unexpected router advertisement (-want +got):\n%s", diff)
	}
}

func TestAdvertiserUnsolicitedShutdown(t *testing.T) {
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

func TestAdvertiserSolicited(t *testing.T) {
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

func TestAdvertiserContextCanceled(t *testing.T) {
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

func testAdvertiser(t *testing.T, cfg *config.Interface) (*Advertiser, *ndp.Conn, net.HardwareAddr, func()) {
	t.Helper()

	// Allow empty config but always populate the interface name.
	// TODO: consider building veth pairs within the tests.
	if cfg == nil {
		cfg = &config.Interface{
			// Fixed interval for multicast advertisements.
			MinInterval: 1 * time.Second,
			MaxInterval: 1 * time.Second,
		}
	}
	cfg.Name = "cradveth0"

	ad, err := NewAdvertiser(*cfg, nil)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skip("skipping, permission denied (run this test with CAP_NET_RAW)")
		}

		// Unfortunately this error isn't exposed as os.ErrNotExist.
		if strings.Contains(err.Error(), "no such network interface") {
			t.Skip("skipping, missing cradveth{0,1} veth pair")
		}

		t.Fatalf("failed to create advertiser: %v", err)
	}

	ifi, err := net.InterfaceByName("cradveth1")
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
	}

	return ad, c, ifi.HardwareAddr, done
}
