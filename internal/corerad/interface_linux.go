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
)

// interfaceIPv6Autoconf enables or disables IPv6 autoconfiguration for the
// given interface on Linux systems, returning the previous state of the
// interface so it can be restored at a later time.
func interfaceIPv6Autoconf(iface string, enable bool) (bool, error) {
	in := []byte("0")
	if enable {
		in = []byte("1")
	}

	file := fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/autoconf", iface)

	// The calling function can provide additional insight and we need to check
	// for permission errors, so no need to wrap these errors.

	// Read the current state before setting a new one.
	out, err := ioutil.ReadFile(file)
	if err != nil {
		return false, err
	}

	if err := ioutil.WriteFile(file, in, 0o644); err != nil {
		return false, err
	}

	// Return the previous state so the caller can restore it later.
	return bytes.Equal(out, []byte("1\n")), nil
}
