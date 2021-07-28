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

package system

import (
	"log"
	"os"
	"testing"
)

func TestDialer_setAutoconf(t *testing.T) {
	tests := []struct {
		name             string
		state            *autoconfState
		setOK, restoreOK bool
	}{
		{
			name: "get error",
			state: &autoconfState{
				getErr: os.ErrPermission,
			},
		},
		{
			name: "set error",
			state: &autoconfState{
				setErr: os.ErrInvalid,
			},
		},
		{
			name: "restore error",
			state: &autoconfState{
				restoreErr: os.ErrInvalid,
			},
			setOK: true,
		},
		{
			name: "set and restore permission denied",
			state: &autoconfState{
				setErr:     os.ErrPermission,
				restoreErr: os.ErrPermission,
			},
			setOK:     true,
			restoreOK: true,
		},
		{
			name: "restore not exist",
			state: &autoconfState{
				restoreErr: os.ErrNotExist,
			},
			setOK:     true,
			restoreOK: true,
		},
		{
			name:      "OK",
			state:     &autoconfState{},
			setOK:     true,
			restoreOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Dialer{
				iface: "test0",
				state: tt.state,
				ll:    log.New(os.Stderr, "", 0),
			}

			restore, err := d.setAutoconf()
			if tt.setOK && err != nil {
				t.Fatalf("failed to set autoconf state: %v", err)
			}
			if !tt.setOK && err == nil {
				t.Fatal("expected a set error, but none occurred")
			}
			if err != nil {
				t.Logf("set err: %v", err)
				return
			}

			err = restore()
			if tt.restoreOK && err != nil {
				t.Fatalf("failed to restore autoconf state: %v", err)
			}
			if !tt.restoreOK && err == nil {
				t.Fatal("expected a restore error, but none occurred")
			}
			if err != nil {
				t.Logf("restore err: %v", err)
			}
		})
	}
}

type autoconfState struct {
	calls                      int
	getErr, setErr, restoreErr error
}

var _ State = &autoconfState{}

func (as *autoconfState) IPv6Autoconf(_ string) (bool, error) { return true, as.getErr }
func (*autoconfState) IPv6Forwarding(_ string) (bool, error) {
	panic("should not call IPv6Forwarding")
}

func (as *autoconfState) SetIPv6Autoconf(_ string, _ bool) error {
	defer func() { as.calls++ }()

	switch as.calls {
	case 0:
		return as.setErr
	case 1:
		return as.restoreErr
	default:
		panic("too many calls to SetIPv6Autoconf")
	}
}
