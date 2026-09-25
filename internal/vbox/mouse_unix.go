//go:build unix

package vbox

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/chryaner/terrarium/internal/paths"
)

// PutMouseEvents injects absolute mouse events into a running VM through
// terrarium's vboxwebsrv, starting it on first use. See webmouse.go.
func (c *Client) PutMouseEvents(vm string, events []MouseEvent, gap time.Duration) error {
	w, err := c.webMouse()
	if err != nil {
		return err
	}
	return w.putEvents(vm, events, gap)
}

// PutMouseWheel turns the wheel without moving the pointer. Positive clicks
// scroll down.
func (c *Client) PutMouseWheel(vm string, clicks int) error {
	w, err := c.webMouse()
	if err != nil {
		return err
	}
	return w.wheel(vm, clicks)
}

func (c *Client) webMouse() (*webMouse, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return nil, err
	}
	secret, err := loadOrCreateSecret(dir)
	if err != nil {
		return nil, fmt.Errorf("web service password: %w", err)
	}
	return &webMouse{
		soap:     &soapClient{endpoint: webEndpoint, http: &http.Client{Timeout: c.Timeout}},
		user:     webUser,
		password: secret,
		ensure:   func() error { return c.startWebService(dir, secret) },
		register: func() error { return c.registerWebUser(secret) },
		vouch:    vouchListener,
	}, nil
}

// vouchListener refuses to log on to a port another user holds. Linux always
// has /proc/net/tcp, so failing to read it there refuses too; other Unixes
// have nothing to check against and log on as before.
func vouchListener() error {
	b, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		if runtime.GOOS == "linux" {
			return fmt.Errorf("cannot tell who holds 127.0.0.1:%d, so terrarium will not send it a password: %w", webPort, err)
		}
		return nil
	}
	if uid, found := listenerUID(string(b), webPort); found && uid != os.Getuid() {
		return &foreignListenerError{port: webPort, uid: uid}
	}
	return nil
}

// startWebService launches vboxwebsrv in its own session so it outlives this
// process, the way VBoxSVC does; the next terrarium command reuses it. Its
// output goes to a log file, never to our stdout, which an MCP server uses
// for protocol traffic.
func (c *Client) startWebService(dir, secret string) error {
	bin, err := webServiceBinary(c.Exe)
	if err != nil {
		return err
	}
	if err := c.registerWebUser(secret); err != nil {
		return err
	}
	logPath := filepath.Join(dir, "vboxwebsrv.log")
	cmd := exec.Command(bin,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(webPort),
		"--authentication", "VBoxAuthSimple",
		"--logfile", logPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", bin, err)
	}
	// Wait reaps the child if it exits while we live, so it never lingers as
	// a zombie; buffered, so the goroutine ends even when nobody reads.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(webPort))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			conn.Close()
			return nil
		}
		select {
		case err := <-exited:
			// A racing terrarium may have won the port; that one serves.
			if conn, derr := net.DialTimeout("tcp", addr, time.Second); derr == nil {
				conn.Close()
				return nil
			}
			return fmt.Errorf("%s exited (%v); see %s", bin, err, logPath)
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s did not listen on %s within 15s; see %s", bin, addr, logPath)
}

// registerWebUser records the password's hash where VBoxAuthSimple looks.
func (c *Client) registerWebUser(secret string) error {
	if _, err := c.run("setextradata", "global", "VBoxAuthSimple/users/"+webUser, authSimpleHash(secret)); err != nil {
		return fmt.Errorf("registering terrarium with VBoxAuthSimple: %w", err)
	}
	return nil
}

// webServiceBinary finds vboxwebsrv, which installs beside VBoxManage.
func webServiceBinary(vboxManage string) (string, error) {
	// Beside the VBoxManage we run first, then beside what it links to:
	// distro packages put wrapper links in /usr/bin and the binaries in
	// /usr/lib/virtualbox.
	dirs := []string{filepath.Dir(vboxManage)}
	if real, err := filepath.EvalSymlinks(vboxManage); err == nil {
		dirs = append(dirs, filepath.Dir(real))
	}
	for _, dir := range dirs {
		p := filepath.Join(dir, "vboxwebsrv")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("vboxwebsrv"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("vboxwebsrv not found beside %s or on PATH: mouse input needs VirtualBox's web service, which ships with the official VirtualBox packages", vboxManage)
}
