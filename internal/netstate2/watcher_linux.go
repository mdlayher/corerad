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

//+build linux

package netstate2

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
func (w *Watcher) osWatch(ctx context.Context) error {
	c, err := rtnetlink.Dial(&netlink.Config{
		// TODO: parameterize.
		Groups: unix.RTMGRP_LINK | unix.RTMGRP_IPV6_IFADDR,
	})
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
		w.process(msgs)
	}
}

// process handles received route netlink messages
func (w *Watcher) process(msgs []rtnetlink.Message) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, m := range msgs {
		// TODO: also inspect other message types for routes, neighbors, etc.
		switch m := m.(type) {
		case *rtnetlink.AddressMessage:
			for _, c := range w.changes[int(m.Index)] {
				c <- &AddressChange{
					Interface: w.indexName[int(m.Index)],
					// TODO: make a copy?
					IP:  m.Attributes.Address,
					sys: m,
				}
			}
		case *rtnetlink.LinkMessage:
			// Guard against nil attributes.
			if m.Attributes == nil {
				continue
			}

			if prev, ok := w.nameIndex[m.Attributes.Name]; !ok || int(m.Index) != prev {
				// Found a new link that we hadn't previously indexed, or the
				// link has come up again with a new index (due to deletion and
				// recreation), add it to the map.
				i := int(m.Index)
				w.indexName[i] = m.Attributes.Name
				w.nameIndex[m.Attributes.Name] = i

				// We also need to move any watcher channels in the tentative
				// map for this interface into the changes map, so they'll
				// receive messages for this change and future ones.
				w.changes[i] = append(w.changes[i], w.tentative[m.Attributes.Name]...)
				delete(w.tentative, m.Attributes.Name)

				// Finally, move any previous change associations into the new
				// and index in the map.
				if ok {
					w.changes[i] = append(w.changes[i], w.changes[prev]...)
					delete(w.changes, prev)

					// Remove the old index to name association after processing
					// the remaining messages as they may refer to the old index
					// for address changes and the like.
					defer delete(w.indexName, prev)
				}
			}

			op, ok := operStateChange(m.Attributes.OperationalState)
			if !ok {
				// Unrecognized value, nothing to do.
				continue
			}

			for _, c := range w.changes[int(m.Index)] {
				c <- &LinkChange{
					Interface: m.Attributes.Name,
					State:     op,
					sys:       m,
				}
			}
		}
	}
}

// operStateChange converts a rtnetlink.OperationalState to an OperationalState
// value.
func operStateChange(s rtnetlink.OperationalState) (OperationalState, bool) {
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
