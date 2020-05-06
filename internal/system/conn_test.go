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

package system

import (
	"errors"
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/nettest"
)

func Test_lookupInterface(t *testing.T) {
	lo, err := nettest.LoopbackInterface()
	if err != nil {
		t.Fatalf("failed to get loopback interface: %v", err)
	}

	tests := []struct {
		name  string
		iface string
		ifi   *net.Interface
		ok    bool
	}{
		{
			name:  "not found",
			iface: "notexist0",
		},
		{
			name:  "OK",
			iface: lo.Name,
			ifi:   lo,
			ok:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ifi, err := lookupInterface(tt.iface)
			if tt.ok && err != nil {
				t.Fatalf("failed to look up interface: %v", err)
			}
			if !tt.ok && !errors.Is(err, errLinkNotReady) {
				t.Fatalf("expected link not ready, but got: %v", err)
			}
			if err != nil {
				t.Logf("err: %v", err)
				return
			}

			if diff := cmp.Diff(tt.ifi, ifi); diff != "" {
				t.Fatalf("unexpected interface (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_checkInterface(t *testing.T) {
	mac := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xde, 0xad}

	tests := []struct {
		name        string
		ifi         *net.Interface
		addrFunc    func() ([]net.Addr, error)
		ok, tempErr bool
	}{
		{
			name: "no MAC",
			ifi: &net.Interface{
				Name: "test0",
			},
		},
		{
			name: "link down",
			ifi: &net.Interface{
				Name:         "test0",
				HardwareAddr: mac,
			},
			tempErr: true,
		},
		{
			name: "failed to get addresses",
			ifi: &net.Interface{
				Name:         "test0",
				HardwareAddr: mac,
				Flags:        net.FlagUp,
			},
			addrFunc: func() ([]net.Addr, error) {
				return nil, errors.New("some error")
			},
		},
		{
			name: "no link-local address",
			ifi: &net.Interface{
				Name:         "test0",
				HardwareAddr: mac,
				Flags:        net.FlagUp,
			},
			addrFunc: func() ([]net.Addr, error) {
				return []net.Addr{
					&net.TCPAddr{},
					&net.IPNet{IP: net.IPv4zero},
					&net.IPNet{IP: net.IPv6loopback},
				}, nil
			},
			tempErr: true,
		},
		{
			name: "OK",
			ifi: &net.Interface{
				Name:         "test0",
				HardwareAddr: mac,
				Flags:        net.FlagUp,
			},
			addrFunc: func() ([]net.Addr, error) {
				return []net.Addr{&net.IPNet{
					IP: net.ParseIP("fe80::1"),
				}}, nil
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkInterface(tt.ifi, tt.addrFunc)
			if tt.ok && err != nil {
				t.Fatalf("failed to check interface: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}

			notReady := errors.Is(err, errLinkNotReady)
			if tt.tempErr && !notReady {
				t.Fatalf("error should be link not ready, but got: %v", err)
			}
			if !tt.tempErr && notReady {
				t.Fatalf("error does not match link not ready: %v", err)
			}
		})
	}
}
