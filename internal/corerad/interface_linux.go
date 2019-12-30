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

//+build linux

package corerad

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
)

// setIPv6Autoconf enables or disables IPv6 autoconfiguration for the
// given interface on Linux systems, returning the previous state of the
// interface so it can be restored at a later time.
func setIPv6Autoconf(iface string, enable bool) (bool, error) {
	// The calling function can provide additional insight and we need to check
	// for permission errors, so no need to wrap these errors.

	// Read the current state before setting a new one.
	prev, err := getIPv6Autoconf(iface)
	if err != nil {
		return false, err
	}

	if err := sysctlEnable(iface, "autoconf", enable); err != nil {
		return false, err
	}

	// Return the previous state so the caller can restore it later.
	return prev, nil
}

// getIPv6Autoconf fetches the current IPv6 autoconfiguration state for the
// given interface on Linux systems.
func getIPv6Autoconf(iface string) (bool, error) {
	return sysctlBool(sysctl(iface, "autoconf"))
}

// setIpv6Forwarding enables or disables IPv6 forwarding for the given interface
// on Linux systems.
func setIPv6Forwarding(iface string, enable bool) error {
	return sysctlEnable(iface, "forwarding", enable)
}

// getIPv6Forwarding fetches the current IPv6 forwarding state for the
// given interface on Linux systems.
func getIPv6Forwarding(iface string) (bool, error) {
	return sysctlBool(sysctl(iface, "forwarding"))
}

// sysctlBool reads a 0/1 boolean value from a file.
func sysctlBool(file string) (bool, error) {
	out, err := ioutil.ReadFile(file)
	if err != nil {
		return false, err
	}

	return bytes.Equal(out, []byte("1\n")), nil
}

// sysctl builds an IPv6 sysctl path for an interface and given key.
func sysctl(iface, key string) string {
	return filepath.Join(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s", iface), key)
}

// sysctlEnable enable or disables a boolean sysctl.
func sysctlEnable(iface, key string, enable bool) error {
	in := []byte("0")
	if enable {
		in = []byte("1")
	}

	return ioutil.WriteFile(sysctl(iface, key), in, 0o644)
}
