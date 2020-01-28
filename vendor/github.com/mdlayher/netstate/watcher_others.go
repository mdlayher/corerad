//+build !linux

package netstate

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// osWatch is the OS-specific portion of a Watcher's Watch method.
func osWatch(_ context.Context, _ func(changeSet)) error {
	return fmt.Errorf("netstate: Watcher not implemented on %q: %w", runtime.GOOS, os.ErrNotExist)
}
