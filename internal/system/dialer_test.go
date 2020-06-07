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

package system_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"testing"
	"time"

	"github.com/mdlayher/corerad/internal/system"
	"inet.af/netaddr"
)

const (
	privileged   = true
	unprivileged = false
)

func TestDialerDialUnprivileged(t *testing.T) {
	t.Parallel()

	d := testDialer(t, unprivileged)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := d.Dial(ctx, func(_ context.Context, _ *system.DialContext) error {
		panic("this should never be called")
	})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected permission denied, but got: %v", err)
	}
}

func TestDialerDialPrivilegedReinitialize(t *testing.T) {
	t.Parallel()

	d := testDialer(t, privileged)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Keep returning a link state change to test reinit logic until an attempt
	// finally succeeds.
	var calls int
	err := d.Dial(ctx, func(_ context.Context, dctx *system.DialContext) error {
		defer func() { calls++ }()
		if calls < 3 {
			return system.ErrLinkChange
		}

		return nil
	})
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
}

func TestDialerDialPrivilegedFatalError(t *testing.T) {
	t.Parallel()

	d := testDialer(t, privileged)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := d.Dial(ctx, func(_ context.Context, dctx *system.DialContext) error {
		// Any reads will immediately time out causing an error we can inspect
		// against net.Error.
		if err := dctx.Conn.SetReadDeadline(time.Unix(1, 0)); err != nil {
			return err
		}

		_, _, _, err := dctx.Conn.ReadFrom()
		return err
	})

	var nerr net.Error
	if !errors.As(err, &nerr) || !nerr.Timeout() {
		t.Fatalf("expected timeout net.Error, but got: %v", err)
	}
}

func TestDialerDialRetryContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	timer := time.AfterFunc(5*time.Second, func() {
		panic("test took too long")
	})
	defer timer.Stop()

	// Create a Dialer which will attempt to dial a connection with backoff and
	// retry due to the link not being ready. This will continue until the
	// context is canceled after a couple of iterations.
	var calls int
	d := system.NewDialer("test0", nil, system.Advertise, log.New(os.Stderr, "", 0))
	d.DialFunc = func() (*system.DialContext, error) {
		defer func() { calls++ }()

		if calls == 2 {
			// Cancel after several retries.
			cancel()
		}

		return nil, system.ErrLinkNotReady
	}

	// Dial should consume the context canceled error as it assumes the caller
	// is no longer interested in retries.
	err := d.Dial(ctx, func(_ context.Context, dctx *system.DialContext) error {
		panic("this should never be called")
	})
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
}

func testDialer(t *testing.T, privileged bool) *system.Dialer {
	curr, err := user.Current()
	if err != nil {
		t.Fatalf("failed to get current user: %v", err)
	}

	switch {
	case !privileged && curr.Uid == "0":
		t.Skip("skipping, this test requires an unprivileged user")
	case privileged && curr.Uid != "0":
		// TODO: is it possible to check capabilities?
		t.Skip("skipping, this test requires a privileged user")
	}

	ifis, err := net.Interfaces()
	if err != nil {
		t.Fatalf("failed to fetch interfaces: %v", err)
	}

	var name string
	for _, ifi := range ifis {
		// Interface must have a MAC address (i.e. not WireGuard or similar) and
		// an IPv6 link-local address to open ndp.Conn.
		if ifi.HardwareAddr == nil {
			continue
		}

		addrs, err := ifi.Addrs()
		if err != nil {
			t.Fatalf("failed to fetch addresses: %v", err)
		}

		var hasLLA bool
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}

			ipp, ok := netaddr.FromStdIPNet(ipn)
			if !ok {
				panicf("system: invalid net.IPNet: %+v", a)
			}

			if ipp.IP.IsLinkLocalUnicast() || ipp.IP.Is6() {
				hasLLA = true
				break
			}
		}
		if hasLLA {
			name = ifi.Name
			break
		}
	}
	if name == "" {
		t.Skip("skipping, no suitable network interface")
	}

	return system.NewDialer(name, system.NewState(), system.Advertise, log.New(os.Stderr, "", 0))
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
