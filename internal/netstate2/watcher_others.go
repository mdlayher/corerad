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

//+build !linux

package netstate2

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// osWatch is the OS-specific portion of a Watcher's Watch method.
func (*Watcher) osWatch(_ context.Context) error {
	return fmt.Errorf("netstate: Watcher not implemented on %q: %w", runtime.GOOS, os.ErrNotExist)
}
