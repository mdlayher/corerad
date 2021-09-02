// Copyright 2020-2021 Matt Layher
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

//go:build linux
// +build linux

package netstate

import (
	"context"
	"fmt"
	"time"

	"github.com/jsimonetti/rtnetlink"
	"github.com/mdlayher/netlink"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

// deadlineNow is a sentinel value which will cause an immediate timeout to
// the rtnetlink listener.
var deadlineNow = time.Unix(0, 1)

// osWatch is the OS-specific portion of a Watcher's Watch method.
func osWatch(ctx context.Context, notify func(changeSet)) error {
	c, err := rtnetlink.Dial(&netlink.Config{Groups: unix.RTMGRP_LINK})
	if err != nil {
		return fmt.Errorf("netstate: watcher failed to dial route netlink: %w", err)
	}
	defer c.Close()

	// Wait for cancelation and then force any pending reads to time out.
	var eg errgroup.Group
	eg.Go(func() error {
		<-ctx.Done()

		if err := c.SetReadDeadline(deadlineNow); err != nil {
			return fmt.Errorf("netstate: failed to interrupt watcher: %w", err)
		}

		return nil
	})

	for {
		msgs, _, err := c.Receive()
		if err != nil {
			if ctx.Err() != nil {
				// Context canceled.
				return eg.Wait()
			}

			return fmt.Errorf("netstate: watcher failed to listen for route netlink messages: %w", err)
		}

		// Received messages; produce a changeSet and notify subscribers.
		notify(process(msgs))
	}
}

// process handles received route netlink messages and produces a changeSet
// suitable for use with the Watcher.notify method.
func process(msgs []rtnetlink.Message) changeSet {
	changes := make(changeSet)
	for _, m := range msgs {
		// TODO: also inspect other message types for addresses, routes, etc.
		switch m := m.(type) {
		case *rtnetlink.LinkMessage:
			// TODO: inspect message header/type?

			// Guard against nil.
			if m.Attributes == nil {
				continue
			}

			c, ok := operStateChange(m.Attributes.OperationalState)
			if !ok {
				// Unrecognized value, nothing to do.
				continue
			}

			iface := m.Attributes.Name
			changes[iface] = append(changes[iface], c)
		}
	}

	return changes
}

// operStateChange converts a rtnetlink.OperationalState to a Change value.
func operStateChange(s rtnetlink.OperationalState) (Change, bool) {
	switch s {
	case rtnetlink.OperStateUnknown:
		return LinkUnknown, true
	case rtnetlink.OperStateNotPresent:
		return LinkNotPresent, true
	case rtnetlink.OperStateDown:
		return LinkDown, true
	case rtnetlink.OperStateLowerLayerDown:
		return LinkLowerLayerDown, true
	case rtnetlink.OperStateTesting:
		return LinkTesting, true
	case rtnetlink.OperStateDormant:
		return LinkDormant, true
	case rtnetlink.OperStateUp:
		return LinkUp, true
	default:
		// Unhandled value, do nothing.
		return 0, false
	}
}
