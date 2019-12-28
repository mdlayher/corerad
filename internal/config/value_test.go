// Copyright 2019 Matt Layher
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

package config

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func Test_value(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in, want interface{}
		fn       func(v *value) interface{}
		ok       bool
	}{
		{
			name: "bad bool",
			fn: func(v *value) interface{} {
				return v.Bool()
			},
			in: 1,
		},
		{
			name: "OK bool",
			fn: func(v *value) interface{} {
				return v.Bool()
			},
			in:   true,
			want: true,
			ok:   true,
		},
		{
			name: "bad string",
			fn: func(v *value) interface{} {
				return v.string()
			},
			in: 1,
		},
		{
			name: "OK string",
			fn: func(v *value) interface{} {
				return v.string()
			},
			in:   "foo",
			want: "foo",
			ok:   true,
		},
		{
			name: "bad Duration type",
			fn: func(v *value) interface{} {
				return v.Duration()
			},
			in: 1,
		},
		{
			name: "bad Duration string",
			fn: func(v *value) interface{} {
				return v.Duration()
			},
			in: "foo",
		},
		{
			name: "OK Duration",
			fn: func(v *value) interface{} {
				return v.Duration()
			},
			in:   "60s",
			want: 1 * time.Minute,
			ok:   true,
		},
		{
			name: "bad IPNet CIDR",
			fn: func(v *value) interface{} {
				return v.IPNet()
			},
			in: "foo/64",
		},
		{
			name: "bad IPNet IP",
			fn: func(v *value) interface{} {
				return v.IPNet()
			},
			in: "2001:db8::1/64",
		},
		{
			name: "bad IPNet IPv4",
			fn: func(v *value) interface{} {
				return v.IPNet()
			},
			in: "192.0.2.0/24",
		},
		{
			name: "OK IPNet",
			fn: func(v *value) interface{} {
				return v.IPNet()
			},
			in:   "2001:db8::/64",
			want: mustCIDR("2001:db8::/64"),
			ok:   true,
		},
		{
			name: "bad IPSlice array",
			fn: func(v *value) interface{} {
				return v.IPSlice()
			},
			in: "foo",
		},
		{
			name: "bad IPSlice array types",
			fn: func(v *value) interface{} {
				return v.IPSlice()
			},
			in: []interface{}{"foo", 1},
		},
		{
			name: "bad IPSlice IP",
			fn: func(v *value) interface{} {
				return v.IPSlice()
			},
			in: []interface{}{"foo"},
		},
		{
			name: "bad IPSlice IPv4",
			fn: func(v *value) interface{} {
				return v.IPSlice()
			},
			in: []interface{}{"192.0.2.1"},
		},
		{
			name: "OK IPSlice",
			fn: func(v *value) interface{} {
				return v.IPSlice()
			},
			in: []interface{}{"2001:db8::1", "2001:db8::2"},
			want: []net.IP{
				mustIP("2001:db8::1"),
				mustIP("2001:db8::2"),
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := value{v: tt.in}

			got := tt.fn(&v)

			err := v.Err()
			if tt.ok && err != nil {
				t.Fatalf("failed to parse value: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				t.Logf("err: %v", err)
				return
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("unexpected output (-want +got):\n%s", diff)
			}
		})
	}
}

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panicf("failed to parse %q as IP address", s)
	}

	return ip
}

func mustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
