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
	"fmt"
	"io/ioutil"
	"log"
	"sync/atomic"
)

// A Watcher watches for interface state changes and notifies listeners via
// a channel.
type Watcher struct {
	// Atomics must come first per sync/atomic.
	watching *uint32

	ll *log.Logger
	m  map[string]chan<- struct{}
}

// NewWatcher creates a Watcher. If ll is nil, logs are discarded.
func NewWatcher(ll *log.Logger) *Watcher {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	return &Watcher{
		watching: new(uint32),

		ll: ll,
		m:  make(map[string]chan<- struct{}),
	}
}

// Register registers interest for state changes on an interface via the input
// channel. The Watcher assumes ownership of the channel once Register is
// invoked, and the caller should no longer send on the channel.
//
// If Register is called after Watch, or Register is called more than once for
// the same interface name, Register panics.
func (w *Watcher) Register(iface string, watchC chan<- struct{}) {
	if _, ok := w.m[iface]; ok {
		panicf("corerad: called Watcher.Register twice for interface %q", iface)
	}

	if atomic.LoadUint32(w.watching) != 0 {
		panic("corerad: called Watcher.Register after Watcher.Watch")
	}

	w.m[iface] = watchC
}

// Watch runs the Watcher until ctx is canceled.
func (w *Watcher) Watch(ctx context.Context) error {
	if v := atomic.SwapUint32(w.watching, 1); v != 0 {
		panic("corerad: multiple calls to Watcher.Watch")
	}

	defer func() {
		// All done, close the registered channels so those listening on them
		// can also clean up.
		for k := range w.m {
			close(w.m[k])
		}
	}()

	return w.watch(ctx)
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
