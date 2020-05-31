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

package corerad

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/ndp"
	"golang.org/x/net/ipv6"
	"inet.af/netaddr"
)

var (
	// deadlineNow causes connection deadlines to trigger immediately.
	deadlineNow = time.Unix(1, 0)

	// errRetriesExhausted is a sentinel which indicates that receiveRetry failed
	// after exhausting its retries.
	errRetriesExhausted = errors.New("exhausted receive retries")
)

// interruptContext returns a function meant for use with errgroup.Group.Go
// which will interrupt conn when ctx is canceled.
func interruptContext(ctx context.Context, conn system.Conn) func() error {
	return func() error {
		<-ctx.Done()

		if err := conn.SetReadDeadline(deadlineNow); err != nil {
			return fmt.Errorf("failed to interrupt listener: %w", err)
		}

		return nil
	}
}

// receiveRetry will attempt to read an NFP message from conn until ctx is
// canceled or it exhausts a fixed number of retries.
func receiveRetry(ctx context.Context, conn system.Conn) (ndp.Message, *ipv6.ControlMessage, netaddr.IP, error) {
	// TODO(mdlayher): consider parameterizing in the future if need be.
	const retries = 5

	for i := 0; i < retries; i++ {
		// Enable cancelation before receiving any messages, if necessary.
		if err := ctx.Err(); err != nil {
			return nil, nil, netaddr.IP{}, err
		}

		m, cm, from, err := conn.ReadFrom()
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				// Context canceled.
				return nil, nil, netaddr.IP{}, cerr
			}

			var nerr net.Error
			if errors.As(err, &nerr) && nerr.Temporary() {
				// Temporary error or timeout, either back off and retry or
				// return if the context is canceled.
				select {
				case <-ctx.Done():
					return nil, nil, netaddr.IP{}, ctx.Err()
				case <-time.After(time.Duration(i) * 50 * time.Millisecond):
				}
				continue
			}

			return nil, nil, netaddr.IP{}, err
		}

		// Convert to netaddr.IP for use elsewhere.
		host, ok := netaddr.FromStdIP(from)
		if !ok {
			panicf("netaddr: invalid IP address: %q", from)
		}

		return m, cm, host, nil
	}

	return nil, nil, netaddr.IP{}, errRetriesExhausted
}
