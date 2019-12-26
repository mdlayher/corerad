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
)

// A value is a raw configuration value which can be unwrapped into a proper
// Go type by calling its methods.
type value struct {
	v   interface{}
	err error
}

// Err returns the error of any type conversions.
func (v *value) Err() error { return v.err }

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

// Bool interprets the value as a bool.
func (v *value) Bool() bool {
	b, ok := v.v.(bool)
	if !ok {
		v.err = errors.New("value must be a bool")
		return false
	}

	return b
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
