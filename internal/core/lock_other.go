//go:build !windows

package core

import "time"

// terrarium is not yet supported on non-Windows hosts; this keeps the package
// building on the Linux CI runner. A POSIX port should replace it with a real
// flock(2)-based lock.
func lockState(path string, timeout time.Duration) (func(), error) {
	return func() {}, nil
}
