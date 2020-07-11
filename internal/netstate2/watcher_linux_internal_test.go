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
	"net"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jsimonetti/rtnetlink"
)

func TestLinuxWatcher(t *testing.T) {
	tests := []struct {
		name    string
		msgs    []rtnetlink.Message
		changes []Change
	}{
		{
			// TODO: break into multiple cases.
			name: "TODO",
			msgs: []rtnetlink.Message{
				// This message is completely ignored.
				&rtnetlink.LinkMessage{
					Index: 10,
					Attributes: &rtnetlink.LinkAttributes{
						Name:             "other0",
						OperationalState: rtnetlink.OperStateUp,
					},
				},
				// This message creates the link association and updates the
				// index so the next message has the appropriate interface name
				// linked with it.
				&rtnetlink.LinkMessage{
					Index: 1,
					Attributes: &rtnetlink.LinkAttributes{
						Name:             "test0",
						OperationalState: rtnetlink.OperStateDown,
					},
				},
				&rtnetlink.AddressMessage{
					Index: 1,
					Attributes: rtnetlink.AddressAttributes{
						Address: net.ParseIP("fe80::1"),
					},
				},
				// Pretend the interface was deleted and recreated with a new
				// index.
				&rtnetlink.LinkMessage{
					Index: 2,
					Attributes: &rtnetlink.LinkAttributes{
						Name:             "test0",
						OperationalState: rtnetlink.OperStateUp,
					},
				},
				&rtnetlink.AddressMessage{
					Index: 2,
					Attributes: rtnetlink.AddressAttributes{
						Address: net.ParseIP("fe80::1"),
					},
				},
			},
			changes: []Change{
				&LinkChange{
					Interface: "test0",
					State:     LinkDown,
				},
				&AddressChange{
					Interface: "test0",
					IP:        net.ParseIP("fe80::1"),
				},
				&LinkChange{
					Interface: "test0",
					State:     LinkUp,
				},
				&AddressChange{
					Interface: "test0",
					IP:        net.ParseIP("fe80::1"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWatcher()

			changeC := w.Subscribe("test0")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Listen for changes and add them to a slice for later comparison.
			var wg sync.WaitGroup
			wg.Add(1)

			var changes []Change
			go func() {
				defer wg.Done()

				var i int
				for c := range changeC {
					changes = append(changes, c)
					i++

					// TODO: this math won't work if more than one message is
					// ignored the input.
					if i == len(tt.msgs)-1 {
						cancel()
					}
				}
			}()

			// Implement the watching function by processing a fixed set of
			// input messages and then returning once the context has been
			// canceled by the client.
			w.watch = func(ctx context.Context) error {
				w.process(tt.msgs)
				<-ctx.Done()
				return nil
			}

			if err := w.Watch(ctx); err != nil {
				t.Fatalf("failed to watch: %v", err)
			}
			wg.Wait()

			// Finally, compare the changes, ignoring the unexported sys data.
			if diff := cmp.Diff(tt.changes, changes, ignoreOpt()); diff != "" {
				t.Fatalf("unexpected changes (-want +got):\n%s", diff)
			}
		})
	}
}

func ignoreOpt() cmp.Option {
	return cmpopts.IgnoreUnexported(AddressChange{}, LinkChange{})
}
