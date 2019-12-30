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
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/ndp"
)

// A value is a raw configuration value which can be unwrapped into a proper
// Go type by calling its methods.
type value struct {
	v   interface{}
	err error
}

// Err returns the error of any type conversions.
func (v *value) Err() error { return v.err }

// Duration interprets the value as a time.Duration.
func (v *value) Duration() time.Duration {
	s := v.string()
	if v.err != nil {
		return 0
	}

	// Allow for "infinite" durations per the NDP RFC.
	if s == "infinite" {
		return ndp.Infinity
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		v.err = err
		return 0
	}

	return d
}

// IPNet interprets the value as an IPv6 *net.IPNet.
func (v *value) IPNet() *net.IPNet {
	s := v.string()
	if v.err != nil {
		return nil
	}

	ip, cidr, err := net.ParseCIDR(s)
	if err != nil {
		v.err = err
		return nil
	}

	// Don't allow individual IP addresses.
	if !cidr.IP.Equal(ip) {
		v.err = fmt.Errorf("%q is not a CIDR prefix", ip)
		return nil
	}

	// Only allow IPv6 addresses.
	if cidr.IP.To16() != nil && cidr.IP.To4() != nil {
		v.err = fmt.Errorf("%q is not an IPv6 CIDR prefix", cidr.IP)
		return nil
	}

	return cidr
}

// IPSlice interprets the value as a []net.IP composed of IPv6 addresses.
func (v *value) IPSlice() []net.IP {
	ss := v.StringSlice()
	if v.err != nil {
		return nil
	}

	ips := make([]net.IP, 0, len(ss))
	for _, s := range ss {
		ip := net.ParseIP(s)
		if ip == nil || (ip.To16() != nil && ip.To4() != nil) {
			v.err = fmt.Errorf("string %q is not an IPv6 address", s)
			return nil
		}

		ips = append(ips, ip)
	}

	return ips
}

// Bool interprets the value as a bool.
func (v *value) Bool() bool {
	b, ok := v.v.(bool)
	if !ok {
		v.err = errors.New("value must be a boolean")
		return false
	}

	return b
}

// Int interprets the value as an int within the specified range.
func (v *value) Int(min, max int) int {
	if max < min {
		panic("max is less than min")
	}

	// Gracefully handle integers in both tests and from the TOML parser.
	var i int
	switch x := v.v.(type) {
	case int:
		i = x
	case int64:
		i = int(x)
	default:
		v.err = errors.New("value must be an integer")
		return 0
	}

	if i < min || i > max {
		v.err = fmt.Errorf("integer %d is not within range %d-%d", i, min, max)
		return 0
	}

	return i
}

// StringSlice interprets the value as a []string.
func (v *value) StringSlice() []string {
	vs, ok := v.v.([]interface{})
	if !ok {
		v.err = errors.New("value must be an array of strings")
		return nil
	}

	ss := make([]string, 0, len(vs))
	for _, vv := range vs {
		s, ok := vv.(string)
		if !ok {
			v.err = errors.New("array values must be strings")
			return nil
		}

		ss = append(ss, s)
	}

	return ss
}

// string interprets the value as a string.
func (v *value) string() string {
	s, ok := v.v.(string)
	if !ok {
		v.err = errors.New("value must be a string")
		return ""
	}

	return s
}
