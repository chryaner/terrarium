//go:build windows

package vbox

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// lockShared attaches to a running VM's session without taking it over
// (VirtualBox LockType_Shared).
const lockShared = 1

// withMouse runs fn against a running VM's IMouse. This is the only COM in
// terrarium: VBoxManage exposes the keyboard but not the mouse, so this one
// path talks to VBoxSVC directly. The connection is short-lived on purpose,
// like a VBoxManage invocation: open, inject, release, so there is no session
// state to leak and no stale handle to VBoxSVC after it idles out.
//
// COM requires thread affinity; goroutines migrate between OS threads, hence
// the LockOSThread bracket around the whole conversation.
func withMouse(vm string, fn func(mouse *ole.IDispatch) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		var oe *ole.OleError
		// S_FALSE: this thread already initialized COM, which is fine and
		// still needs the matching CoUninitialize.
		if !errors.As(err, &oe) || oe.Code() != 1 {
			return fmt.Errorf("initializing COM: %w", err)
		}
	}
	defer ole.CoUninitialize()

	unk, err := oleutil.CreateObject("VirtualBox.VirtualBox")
	if err != nil {
		return fmt.Errorf("connecting to VirtualBox COM: %w", err)
	}
	defer unk.Release()
	vbx, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer vbx.Release()

	mv, err := oleutil.CallMethod(vbx, "FindMachine", vm)
	if err != nil {
		return fmt.Errorf("finding VM %q: %w", vm, err)
	}
	machine := mv.ToIDispatch()
	defer machine.Release()

	sunk, err := oleutil.CreateObject("VirtualBox.Session")
	if err != nil {
		return err
	}
	defer sunk.Release()
	sess, err := sunk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer sess.Release()

	if _, err := oleutil.CallMethod(machine, "LockMachine", sess, lockShared); err != nil {
		return fmt.Errorf("attaching to %q (is it running?): %w", vm, err)
	}
	defer oleutil.CallMethod(sess, "UnlockMachine")

	cv, err := oleutil.GetProperty(sess, "Console")
	if err != nil {
		return err
	}
	console := cv.ToIDispatch()
	if console == nil {
		return fmt.Errorf("VM %q has no console: it is not running", vm)
	}
	defer console.Release()

	mo, err := oleutil.GetProperty(console, "Mouse")
	if err != nil {
		return err
	}
	mouse := mo.ToIDispatch()
	defer mouse.Release()

	return fn(mouse)
}

// PutMouseEvents injects absolute mouse events into a running VM, waiting gap
// between them so a press and its release read as a click, not a glitch.
// Needs the VM to have an absolute pointing device (USB tablet); terrarium's
// own VMs do. The API counts pixels from [1,1] while screenshots count from
// [0,0], so the +1 keeps callers in screenshot coordinates.
func (c *Client) PutMouseEvents(vm string, events []MouseEvent, gap time.Duration) error {
	return withMouse(vm, func(mouse *ole.IDispatch) error {
		// Envs forked before terrarium set up USB tablets are PS/2-only, and
		// absolute events would vanish without a trace. Fail with the fix.
		if av, err := oleutil.GetProperty(mouse, "AbsoluteSupported"); err == nil {
			if supported, isBool := av.Value().(bool); isBool && !supported {
				return fmt.Errorf("VM %q has no absolute pointing device: re-fork the env, or power it off and run: VBoxManage modifyvm %s --mouse usbtablet", vm, vm)
			}
		}
		for i, ev := range events {
			if i > 0 && gap > 0 {
				time.Sleep(gap)
			}
			if _, err := oleutil.CallMethod(mouse, "PutMouseEventAbsolute",
				ev.X+1, ev.Y+1, 0, 0, ev.Buttons); err != nil {
				return fmt.Errorf("mouse event %d/%d: %w", i+1, len(events), err)
			}
		}
		return nil
	})
}

// PutMouseWheel turns the wheel without moving the pointer, via a relative
// event with zero movement. Positive clicks scroll down.
func (c *Client) PutMouseWheel(vm string, clicks int) error {
	return withMouse(vm, func(mouse *ole.IDispatch) error {
		_, err := oleutil.CallMethod(mouse, "PutMouseEvent", 0, 0, clicks, 0, 0)
		return err
	})
}
