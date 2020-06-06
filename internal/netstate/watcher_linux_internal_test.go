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

package netstate

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jsimonetti/rtnetlink"
)

func Test_process(t *testing.T) {
	tests := []struct {
		name string
		msgs []rtnetlink.Message
		want changeSet
	}{
		{
			name: "link",
			msgs: []rtnetlink.Message{
				newLink(rtnetlink.OperStateUnknown),
				newLink(rtnetlink.OperStateNotPresent),
				newLink(rtnetlink.OperStateDown),
				newLink(rtnetlink.OperStateLowerLayerDown),
				newLink(rtnetlink.OperStateTesting),
				newLink(rtnetlink.OperStateDormant),
				newLink(rtnetlink.OperStateUp),
				// Ensure another interface's data shows up.
				&rtnetlink.LinkMessage{
					Attributes: &rtnetlink.LinkAttributes{
						Name:             "test1",
						OperationalState: rtnetlink.OperStateDown,
					},
				},
				// Messages which have no effect on the output.
				newLink(0xff),
				&rtnetlink.LinkMessage{Attributes: nil},
			},
			want: map[string][]Change{
				"test0": {
					LinkUnknown,
					LinkNotPresent,
					LinkDown,
					LinkLowerLayerDown,
					LinkTesting,
					LinkDormant,
					LinkUp,
				},
				"test1": {
					LinkDown,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, process(tt.msgs)); diff != "" {
				t.Fatalf("unexpected change set (-want +got):\n%s", diff)
			}
		})
	}
}

func newLink(op rtnetlink.OperationalState) rtnetlink.Message {
	return &rtnetlink.LinkMessage{
		Attributes: &rtnetlink.LinkAttributes{
			Name:             "test0",
			OperationalState: op,
		},
	}
}
