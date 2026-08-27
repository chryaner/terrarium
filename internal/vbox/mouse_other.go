//go:build !windows

package vbox

import (
	"fmt"
	"time"
)

// Mouse injection rides VirtualBox's COM API, which only exists on Windows.
// Terrarium already requires a Windows host; these stubs just keep the
// package compiling on CI's Linux runner.

func (c *Client) PutMouseEvents(vm string, events []MouseEvent, gap time.Duration) error {
	return fmt.Errorf("mouse injection requires a Windows host")
}

func (c *Client) PutMouseWheel(vm string, clicks int) error {
	return fmt.Errorf("mouse injection requires a Windows host")
}
