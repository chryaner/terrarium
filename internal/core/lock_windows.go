//go:build windows

package core

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// lockState takes an exclusive advisory lock on a dedicated lock file so two
// terrarium processes - a CLI command and a running MCP server - cannot write
// state.json at the same time. It is held only for the moment of a Save, never
// across a long install, so contention is a few milliseconds. The lock is a
// separate file, not state.json itself: Save replaces state.json by rename,
// which would drop a lock taken on the file being replaced.
func lockState(path string, timeout time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	deadline := time.Now().Add(timeout)
	for {
		var ol windows.Overlapped
		err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
		if err == nil {
			return func() {
				var ol windows.Overlapped
				windows.UnlockFileEx(h, 0, 1, 0, &ol)
				f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("another terrarium is busy (state locked); try again in a moment")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
