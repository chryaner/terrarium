package vbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Outside Windows there is no COM, and VBoxManage has no mouse verb, so mouse
// injection goes through VirtualBox's web service instead. terrarium runs its
// own instance: loopback only, on a port of its own so it never collides with
// a vboxwebsrv the user runs for something else, and with VBoxAuthSimple and
// a generated password so another local user cannot drive this user's VMs
// through it. The auth library is a per-process flag, which leaves the user's
// global VirtualBox settings alone.
const (
	webPort    = 42199
	webUser    = "terrarium"
	secretFile = "vboxweb.secret"
)

var webEndpoint = fmt.Sprintf("http://127.0.0.1:%d/", webPort)

// webMouse drives IMouse over SOAP. ensure brings the service up when it
// cannot be reached; it is called at most once per operation.
type webMouse struct {
	soap     *soapClient
	user     string
	password string
	ensure   func() error
}

// mouseRef is the managed-object reference of a running VM's IMouse.
type mouseRef string

// with logs on, attaches to the running VM with a shared lock (the one a
// second GUI would take), and hands fn its IMouse. It always unlocks and logs
// off, so nothing is left for the service's session timeout to reap.
func (w *webMouse) with(vm string, fn func(mouseRef) error) error {
	vbox, err := w.soap.call("IWebsessionManager_logon", arg("username", w.user), arg("password", w.password))
	if isTransport(err) {
		if serr := w.ensure(); serr != nil {
			return fmt.Errorf("VirtualBox web service is not running and could not be started: %w", serr)
		}
		vbox, err = w.soap.call("IWebsessionManager_logon", arg("username", w.user), arg("password", w.password))
	}
	if isTransport(err) {
		return fmt.Errorf("VirtualBox web service at %s: %w", w.soap.endpoint, err)
	}
	if err != nil {
		return fmt.Errorf("the VirtualBox web service at %s refused terrarium's login (%w): something else holds that port, or it runs with an older password - stop it with `pkill vboxwebsrv` and try again", w.soap.endpoint, err)
	}
	defer w.soap.call("IWebsessionManager_logoff", arg("refIVirtualBox", vbox))

	machine, err := w.soap.call("IVirtualBox_findMachine", arg("_this", vbox), arg("nameOrId", vm))
	if err != nil {
		return fmt.Errorf("finding VM %q: %w", vm, err)
	}
	session, err := w.soap.call("IWebsessionManager_getSessionObject", arg("refIVirtualBox", vbox))
	if err != nil {
		return err
	}
	if _, err := w.soap.call("IMachine_lockMachine", arg("_this", machine), arg("session", session), arg("lockType", "Shared")); err != nil {
		return fmt.Errorf("attaching to %q (is it running?): %w", vm, err)
	}
	defer w.soap.call("ISession_unlockMachine", arg("_this", session))

	console, err := w.soap.call("ISession_getConsole", arg("_this", session))
	if err != nil {
		return err
	}
	if console == "" {
		return fmt.Errorf("VM %q has no console: it is not running", vm)
	}
	mouse, err := w.soap.call("IConsole_getMouse", arg("_this", console))
	if err != nil {
		return err
	}
	return fn(mouseRef(mouse))
}

// putEvents mirrors the COM path in mouse_windows.go: same tablet check, same
// +1 from screenshot pixels to the API's 1-based coordinates.
func (w *webMouse) putEvents(vm string, events []MouseEvent, gap time.Duration) error {
	return w.with(vm, func(m mouseRef) error {
		if abs, err := w.soap.call("IMouse_getAbsoluteSupported", arg("_this", string(m))); err == nil && abs == "false" {
			return fmt.Errorf("VM %q has no absolute pointing device: re-fork the env, or power it off and run: VBoxManage modifyvm %s --mouse usbtablet", vm, vm)
		}
		for i, ev := range events {
			if i > 0 && gap > 0 {
				time.Sleep(gap)
			}
			if _, err := w.soap.call("IMouse_putMouseEventAbsolute", arg("_this", string(m)),
				arg("x", ev.X+1), arg("y", ev.Y+1), arg("dz", 0), arg("dw", 0), arg("buttonState", ev.Buttons)); err != nil {
				return fmt.Errorf("mouse event %d/%d: %w", i+1, len(events), err)
			}
		}
		return nil
	})
}

// wheel turns the wheel without moving the pointer, like the COM path.
func (w *webMouse) wheel(vm string, clicks int) error {
	return w.with(vm, func(m mouseRef) error {
		_, err := w.soap.call("IMouse_putMouseEvent", arg("_this", string(m)),
			arg("dx", 0), arg("dy", 0), arg("dz", clicks), arg("dw", 0), arg("buttonState", 0))
		return err
	})
}

// authSimpleHash is how VBoxAuthSimple stores a password: hex SHA-256 in
// global extradata under VBoxAuthSimple/users/<name>.
func authSimpleHash(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

// loadOrCreateSecret returns the web service password kept in dir, making one
// on first use. Owner-only, like the SSH keys beside it.
func loadOrCreateSecret(dir string) (string, error) {
	path := filepath.Join(dir, secretFile)
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(raw[:])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", err
	}
	return secret, nil
}
