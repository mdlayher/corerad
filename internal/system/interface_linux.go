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

package system

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
)

// setIPv6Autoconf enables or disables IPv6 autoconfiguration for the
// given interface on Linux systems.
func setIPv6Autoconf(iface string, enable bool) error {
	return sysctlEnable(iface, "autoconf", enable)
}

// getIPv6Autoconf fetches the current IPv6 autoconfiguration state for the
// given interface on Linux systems.
func getIPv6Autoconf(iface string) (bool, error) {
	return sysctlBool(sysctl(iface, "autoconf"))
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
