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
	"net"
	"time"

	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/ndp"
	"golang.org/x/sync/errgroup"
	"inet.af/netaddr"
	"tailscale.com/util/netconv"
)

var (
	// deadlineNow causes connection deadlines to trigger immediately.
	deadlineNow = time.Unix(1, 0)

	// errRetriesExhausted is a sentinel which indicates that receiveRetry failed
	// after exhausting its retries.
	errRetriesExhausted = errors.New("exhausted receive retries")
)

// A listener instruments a system.Conn and adds retry functionality for
// receiving NDP messages.
type listener struct {
	cctx  *Context
	iface string
	c     system.Conn
}

// newListener constructs a listener with optional logger and metrics.
func newListener(cctx *Context, iface string, conn system.Conn) *listener {
	return &listener{
		cctx:  cctx,
		iface: iface,
		c:     conn,
	}
}

// A message contains information from a single NDP read.
type message struct {
	Message ndp.Message
	Host    netaddr.IP
}

// Listen receives NDP messages and invokes onMessage for each until ctx is
// canceled.
func (l *listener) Listen(ctx context.Context, onMessage func(msg message) error) error {
	// Ensure the interrupt goroutine is canceled whether or not ctx itself
	// is canceled.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Wait for cancelation and then force any pending reads to time out.
	var eg errgroup.Group
	eg.Go(func() error {
		<-ctx.Done()

		if err := l.c.SetReadDeadline(deadlineNow); err != nil {
			return fmt.Errorf("failed to interrupt listener: %w", err)
		}

		return nil
	})
	defer func() { _ = eg.Wait() }()

	for {
		// Receive and pass incoming NDP messages to the caller.
		m, host, err := l.receiveRetry(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// Context canceled.
				return eg.Wait()
			}

			return fmt.Errorf("failed to read NDP messages: %w", err)
		}

		msg := message{
			Message: m,
			Host:    host,
		}

		if err := onMessage(msg); err != nil {
			return err
		}
	}
}

// receiveRetry will attempt to read an NDP message from conn until ctx is
// canceled or it exhausts a fixed number of retries.
func (l *listener) receiveRetry(ctx context.Context) (ndp.Message, netaddr.IP, error) {
	// TODO(mdlayher): consider parameterizing in the future if need be.
	const retries = 5

	for i := 0; i < retries; i++ {
		// Enable cancelation before receiving any messages, if necessary.
		if err := ctx.Err(); err != nil {
			return nil, netaddr.IP{}, err
		}

		m, cm, from, err := l.c.ReadFrom()
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				// Context canceled.
				return nil, netaddr.IP{}, cerr
			}

			var nerr net.Error
			if errors.As(err, &nerr) && nerr.Temporary() {
				// Temporary error or timeout, either back off and retry or
				// return if the context is canceled.
				select {
				case <-ctx.Done():
					return nil, netaddr.IP{}, ctx.Err()
				case <-time.After(time.Duration(i) * 50 * time.Millisecond):
				}
				continue
			}

			return nil, netaddr.IP{}, err
		}

		// Convert to netaddr.IP for use elsewhere.
		host := netconv.AsIP(from)

		// Ensure this message has a valid hop limit.
		if cm.HopLimit != ndp.HopLimit {
			l.logf("received NDP message with IPv6 hop limit %d from %s, ignoring", cm.HopLimit, host)
			l.cctx.mm.MessagesReceivedInvalidTotal(1.0, l.iface, m.Type().String())
			continue
		}

		return m, host, nil
	}

	return nil, netaddr.IP{}, errRetriesExhausted
}

// logf prints a formatted log with the listener's interface name.
func (l *listener) logf(format string, v ...interface{}) {
	l.cctx.ll.Printf(l.iface+": "+format, v...)
}
