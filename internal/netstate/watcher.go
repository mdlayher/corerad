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

package netstate

import (
	"context"
	"sync"
	"sync/atomic"
)

// A changeMap is a map of:
//  interface names -> Change subscription bitmasks -> slice of Change channels
//
// When network interface state changes, any callers interested in a certain
// type of Change will be notified via the Change channels.
type changeMap map[string]map[Change][]chan<- Change

// A changeSet is a map of interface names to accumulated Changes to that
// network interface.
type changeSet map[string][]Change

// A Watcher watches for interface state changes and notifies listeners via
// a channel.
type Watcher struct {
	// Atomics must come first per sync/atomic.
	watching *uint32

	// Track Change subscribers.
	mu sync.RWMutex
	m  changeMap

	// Swappable watch hook for testing. notify notifies subscribers that the
	// input changes have occurred.
	watch func(ctx context.Context, notify func(changes changeSet)) error
}

// NewWatcher creates a Watcher.
func NewWatcher() *Watcher {
	return &Watcher{
		watching: new(uint32),

		m: make(changeMap),

		// By default, watch using OS-specific primitives.
		watch: osWatch,
	}
}

// Subscribe registers interest for the specified bitmask of state changes on a
// network interface, returning a buffered channel of Changes. The channel will
// be closed when the context passed to Watch is canceled. If the caller does
// not drain Change events from the channel and it reaches capacity, they will
// be dropped.
func (w *Watcher) Subscribe(iface string, changes Change) <-chan Change {
	// TODO: allow unsubscription?

	w.mu.Lock()
	defer w.mu.Unlock()

	changeC := make(chan Change, 8)
	if _, ok := w.m[iface]; !ok {
		w.m[iface] = make(map[Change][]chan<- Change)
	}

	// This caller will now receive notifications for the specified changes on
	// this interface.
	w.m[iface][changes] = append(w.m[iface][changes], changeC)

	return changeC
}

// Watch runs the Watcher and blocks until the specified context is canceled,
// or an error occurs.
//
// If Watch is not supported on the current operating system, it will return
// an error which can be checked using errors.Is(err, os.ErrNotExist).
//
// If Watch is called multiple times, it will panic.
func (w *Watcher) Watch(ctx context.Context) error {
	// TODO: allow reuse of Watcher?
	if v := atomic.SwapUint32(w.watching, 1); v != 0 {
		panic("netstate: multiple calls to Watcher.Watch")
	}

	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()

		// All done, close the registered channels so those listening on them
		// can also clean up.
		for _, v := range w.m {
			for _, vv := range v {
				for _, ch := range vv {
					close(ch)
				}
			}
		}
	}()

	// Call into OS-specific watching code.
	return w.watch(ctx, w.notify)
}

// notify accepts a changeSet of interfaces mapped to their Changes and notifies
// any subscribers who have registered interest for that type of notification.
func (w *Watcher) notify(changed changeSet) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Notify anyone listening that a change has occurred on interfaces
	// which are being watched.
	for iface, changes := range changed {
		interest, ok := w.m[iface]
		if !ok {
			// No interest in this interface.
			continue
		}

		for _, change := range changes {
			for k, v := range interest {
				// Have one or more callers registered interest in this
				// particular change?
				if k&change == 0 {
					// No, do nothing.
					continue
				}

				// Yes, notify callers of this event but do not block if the
				// buffered channel is full.
				for _, ch := range v {
					select {
					case ch <- change:
					default:
					}
				}
			}
		}
	}
}
