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

package corerad

import (
	"context"
	"fmt"

	"github.com/jsimonetti/rtnetlink"
	"github.com/mdlayher/netlink"
	"golang.org/x/sync/errgroup"
)

// watch is the OS-specific portion of Watch.
func (w *Watcher) watch(ctx context.Context) error {
	c, err := rtnetlink.Dial(&netlink.Config{
		Groups: 0x1, // RTMGRP_LINK (TODO: move to x/sys)
	})
	if err != nil {
		return fmt.Errorf("watcher failed to dial route netlink: %v", err)
	}
	defer c.Close()

	// Wait for cancelation and then force any pending reads to time out.
	var eg errgroup.Group
	eg.Go(func() error {
		<-ctx.Done()

		if err := c.SetReadDeadline(deadlineNow); err != nil {
			return fmt.Errorf("failed to interrupt watcher: %w", err)
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

			return fmt.Errorf("watcher failed to listen for route netlink messages: %w", err)
		}

		w.process(msgs)
	}
}

// process handles received netlink messages and notifies listeners.
func (w *Watcher) process(msgs []rtnetlink.Message) error {
	// Track the unique interfaces which changed.
	changed := make(map[string]struct{})
	for _, m := range msgs {
		// TODO: also inspect IP address changes.
		switch m := m.(type) {
		case *rtnetlink.LinkMessage:
			// TODO: inspect more information.
			changed[m.Attributes.Name] = struct{}{}
		}
	}

	// And notify anyone listening that a change has occurred on interfaces
	// which are being watched.
	for k := range changed {
		select {
		case w.m[k] <- struct{}{}:
		default:
		}
	}

	return nil
}
