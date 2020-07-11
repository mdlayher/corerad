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

package netstate2

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
)

// A Watcher watches for interface state changes and notifies listeners via
// a channel.
type Watcher struct {
	// Atomics must come first per sync/atomic.
	watching uint32

	// Guards all of the following maps.
	mu sync.Mutex
	// An index and reverse index of interface names and indices.
	nameIndex map[string]int
	indexName map[int]string
	// Maps of change channels, where "changes" means an interface is already
	// created and can receive events and "tentative" indicates the interface
	// does not exist currently, but may in the future.
	tentative map[string][]chan<- Change
	changes   map[int][]chan<- Change

	// watch is a function which will normally call OS-specific primitives to
	// watch for changes, but can also be swapped for testing.
	watch func(ctx context.Context) error
}

// NewWatcher creates a Watcher.
func NewWatcher() *Watcher {
	w := &Watcher{
		nameIndex: make(map[string]int),
		indexName: make(map[int]string),
		tentative: make(map[string][]chan<- Change),
		changes:   make(map[int][]chan<- Change),
	}

	// By default, watch with OS-specific code.
	w.watch = w.osWatch

	return w
}

// OperationalState describes the operational state of a network interface.
type OperationalState int

// Possible state changes which may occur to a network interface.
const (
	// RFC 2863 "ifOperStatus" values which indicate network interface
	// state changes.
	LinkUp OperationalState = iota
	LinkDown
	LinkTesting
	LinkUnknown
	LinkDormant
	LinkNotPresent
	LinkLowerLayerDown
)

// A Change is an event created by the Watcher that indicates a network device
// state change, such as interface up/down or IP address addition/removal.
type Change interface {
	// Sys returns the operating system-specific representation of a Change's
	// data, such as a raw route netlink message on Linux. Use of Sys must be
	// guarded by build tags in cross-platform code.
	Sys() interface{}
}

var (
	_ Change = &AddressChange{}
	_ Change = &LinkChange{}
)

// An AddressChange is a Change relating to an interface's IP address(es).
type AddressChange struct {
	Interface string
	IP        net.IP
	sys       interface{}
}

// Sys implements Change.
func (c *AddressChange) Sys() interface{} { return c.sys }

// A LinkChange is a Change relating to a network interface's link state.
type LinkChange struct {
	Interface string
	State     OperationalState
	sys       interface{}
}

// Sys implements Change.
func (c *LinkChange) Sys() interface{} { return c.sys }

// Subscribe registers interest for Changes on a network interface, returning a
// buffered channel of Changes. The channel will be closed when the context
// passed to Watch is canceled. If the caller does not drain Change events from
// the channel and it reaches capacity, they will be dropped.
func (w *Watcher) Subscribe(iface string) <-chan Change {
	// TODO: allow unsubscription?
	w.mu.Lock()
	defer w.mu.Unlock()

	changeC := make(chan Change, 8)

	// Does the interface already exist?
	if ifi, err := net.InterfaceByName(iface); err == nil {
		// Yes, we can immediately associate its name/index and begin sending
		// changes on that channel.
		w.nameIndex[iface] = ifi.Index
		w.indexName[ifi.Index] = iface
		w.changes[ifi.Index] = append(w.changes[ifi.Index], changeC)
	} else {
		// No, we must determine the interface index later and will mark this
		// channel as tentative since it cannot receive any current events.
		w.tentative[iface] = append(w.tentative[iface], changeC)
	}

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
	if v := atomic.SwapUint32(&w.watching, 1); v != 0 {
		panic("netstate: multiple calls to Watcher.Watch")
	}

	defer func() {
		// When watch returns, close all subscribed channels and clean up the
		// subscription maps.
		w.mu.Lock()
		defer w.mu.Unlock()

		for k, cs := range w.changes {
			for _, c := range cs {
				close(c)
			}
			delete(w.changes, k)
		}

		for k, cs := range w.tentative {
			for _, c := range cs {
				close(c)
			}
			delete(w.tentative, k)
		}
	}()

	return w.watch(ctx)
}
