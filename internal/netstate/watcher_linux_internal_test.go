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
				"test0": []Change{
					LinkUnknown,
					LinkNotPresent,
					LinkDown,
					LinkLowerLayerDown,
					LinkTesting,
					LinkDormant,
					LinkUp,
				},
				"test1": []Change{
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
