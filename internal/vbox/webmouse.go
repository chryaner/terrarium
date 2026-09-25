package vbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// webMouse drives IMouse over SOAP. Each hook runs at most once per
// operation:
//   - ensure brings the service up when nothing answers;
//   - register writes this password's hash for VBoxAuthSimple, which reads
//     it at every login, so a service holding an older one heals in place.
//
// On Linux the soap client's connections are vouched for before any byte is
// sent (see vouchingClient), so the password never reaches a port another
// local user got to first.
type webMouse struct {
	soap     *soapClient
	user     string
	password string
	ensure   func() error
	register func() error
}

func (w *webMouse) logon() (string, error) {
	return w.soap.call("IWebsessionManager_logon", arg("username", w.user), arg("password", w.password))
}

// mouseRef is the managed-object reference of a running VM's IMouse.
type mouseRef string

// with logs on, attaches to the running VM with a shared lock (the one a
// second GUI would take), and hands fn its IMouse. It always unlocks and logs
// off, so nothing is left for the service's session timeout to reap.
func (w *webMouse) with(vm string, fn func(mouseRef) error) error {
	// A connection refused for its owner is final: starting a service cannot
	// take the port back, and retrying would only knock again.
	var refused *untrustedListenerError
	vbox, err := w.logon()
	if isTransport(err) && !errors.As(err, &refused) {
		if serr := w.ensure(); serr != nil {
			return fmt.Errorf("VirtualBox web service is not running and could not be started: %w", serr)
		}
		vbox, err = w.logon()
	}
	if errors.As(err, &refused) {
		return refused
	}
	if isTransport(err) {
		return fmt.Errorf("VirtualBox web service at %s: %w", w.soap.endpoint, err)
	}
	if err != nil {
		if rerr := w.register(); rerr != nil {
			return fmt.Errorf("the VirtualBox web service refused terrarium's login (%v), and re-registering its password failed: %w", err, rerr)
		}
		vbox, err = w.logon()
	}
	if err != nil {
		return fmt.Errorf("the VirtualBox web service at %s refused terrarium's login (%w): another program holds that port, or it was not started by terrarium - stop it with `pkill -f 'vboxwebsrv.*--port %d'` and try again", w.soap.endpoint, err, webPort)
	}
	defer w.soap.call("IWebsessionManager_logoff", arg("refIVirtualBox", vbox))

	machine, err := w.soap.call("IVirtualBox_findMachine", arg("_this", vbox), arg("nameOrId", vm))
	if err != nil {
		return fmt.Errorf("finding VM %q: %w", vm, err)
	}
	session, err := w.soap.call("IWebsessionManager_getSessionObject", arg("refIVirtualBox", vbox))
	if err != nil {
		return fmt.Errorf("opening a session for %q: %w", vm, err)
	}
	if _, err := w.soap.call("IMachine_lockMachine", arg("_this", machine), arg("session", session), arg("lockType", "Shared")); err != nil {
		return fmt.Errorf("attaching to %q (is it running?): %w", vm, err)
	}
	defer w.soap.call("ISession_unlockMachine", arg("_this", session))

	console, err := w.soap.call("ISession_getConsole", arg("_this", session))
	if err != nil {
		return fmt.Errorf("reaching the console of %q: %w", vm, err)
	}
	if console == "" {
		return fmt.Errorf("VM %q has no console: it is not running", vm)
	}
	mouse, err := w.soap.call("IConsole_getMouse", arg("_this", console))
	if err != nil {
		return fmt.Errorf("reaching the mouse of %q: %w", vm, err)
	}
	return fn(mouseRef(mouse))
}

// putEvents mirrors the COM path in mouse_windows.go.
func (w *webMouse) putEvents(vm string, events []MouseEvent, gap time.Duration) error {
	return w.with(vm, func(m mouseRef) error {
		if abs, err := w.soap.call("IMouse_getAbsoluteSupported", arg("_this", string(m))); err == nil && abs == "false" {
			return errNoTablet(vm)
		}
		for i, ev := range events {
			if i > 0 && gap > 0 {
				time.Sleep(gap)
			}
			x, y := ev.apiXY()
			if _, err := w.soap.call("IMouse_putMouseEventAbsolute", arg("_this", string(m)),
				arg("x", x), arg("y", y), arg("dz", 0), arg("dw", 0), arg("buttonState", ev.Buttons)); err != nil {
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
// on first use. Owner-only, like the SSH keys beside it. The CLI and an MCP
// server can get here at the same moment, so the file is published with a
// link that fails if it exists: exactly one secret wins, and the loser reads
// it instead of registering a hash nobody will send.
func loadOrCreateSecret(dir string) (string, error) {
	path := filepath.Join(dir, secretFile)
	for range 3 {
		b, err := os.ReadFile(path)
		switch {
		case err == nil && strings.TrimSpace(string(b)) != "":
			return strings.TrimSpace(string(b)), nil
		case err == nil:
			// Empty is unusable; replace it.
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		case !errors.Is(err, os.ErrNotExist):
			return "", err
		}
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		secret := hex.EncodeToString(raw[:])
		won, err := publishOnce(dir, path, secret+"\n")
		if err != nil {
			return "", err
		}
		if won {
			return secret, nil
		}
	}
	return "", fmt.Errorf("could not settle on a password in %s", path)
}

// publishOnce writes content to path only if path does not exist yet, and
// never leaves a partly written file there for a reader to see.
func publishOnce(dir, path, content string) (bool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".secret-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	switch err := os.Link(tmp.Name(), path); {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrExist):
		return false, nil
	default:
		return false, err
	}
}

// untrustedListenerError refuses a web service connection whose other end
// this user cannot be shown to own.
type untrustedListenerError struct {
	port int
	uid  int // -1 when the owner could not be found
}

func (e *untrustedListenerError) Error() string {
	if e.uid < 0 {
		return fmt.Sprintf("cannot tell who accepted the connection to 127.0.0.1:%d, so terrarium will not send it a password", e.port)
	}
	return fmt.Sprintf("127.0.0.1:%d is held by another user (uid %d), not a vboxwebsrv of yours; terrarium will not send it a password. Find it with `ss -ltnp 'sport = :%d'`", e.port, e.uid, e.port)
}

// Loopback as /proc/net/tcp prints it, and as /proc/net/tcp6 prints the
// v4-mapped address a dual-stack listener sees.
const (
	procLoopback4       = "0100007F"
	procLoopback4Mapped = "0000000000000000FFFF00000100007F"
)

// peerUID finds who owns the server end of the loopback connection from
// clientPort to serverPort, in the text of /proc/net/tcp and /proc/net/tcp6.
// A socket accepted by a listener carries the listener's uid, so this names
// the real peer however it was bound (127.0.0.1, 0.0.0.0 or ::), and, being
// read after the connection exists, leaves no window to swap listeners.
func peerUID(tables []string, serverPort, clientPort int) (uid int, found bool) {
	local := fmt.Sprintf(":%04X", serverPort)
	remote := map[string]bool{
		fmt.Sprintf("%s:%04X", procLoopback4, clientPort):       true,
		fmt.Sprintf("%s:%04X", procLoopback4Mapped, clientPort): true,
	}
	for _, table := range tables {
		for _, line := range strings.Split(table, "\n") {
			f := strings.Fields(line)
			// sl local rem st tx:rx tr:when retrnsmt uid ...; 01 is
			// ESTABLISHED, which also rules out a TIME_WAIT leftover on the
			// same ports.
			if len(f) < 8 || !strings.HasSuffix(f[1], local) || !remote[f[2]] || f[3] != "01" {
				continue
			}
			if n, err := strconv.Atoi(f[7]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}
