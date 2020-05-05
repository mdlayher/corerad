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

// State is a type which can manipulate the low-level IPv6 parameters of
// a system.
type State interface {
	IPv6Autoconf(iface string) (bool, error)
	IPv6Forwarding(iface string) (bool, error)
}

// NewState creates State which directly manipulates the operating system.
func NewState() State { return systemState{} }

// A systemState directly manipulates the operating system's state.
type systemState struct{}

var _ State = systemState{}

func (systemState) IPv6Autoconf(iface string) (bool, error)   { return getIPv6Autoconf(iface) }
func (systemState) IPv6Forwarding(iface string) (bool, error) { return getIPv6Forwarding(iface) }
