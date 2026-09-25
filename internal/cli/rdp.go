package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/spf13/cobra"
)

var (
	rdpNoLaunch bool
	rdpFull     bool
	rdpMultimon bool
	rdpSize     string
)

// rdpDisplay is the display choice from the flags, before it is spelled for
// a particular client. The session resolution is negotiated once at connect
// time - a mstsc window resized afterwards scrolls or scales, it does not
// renegotiate - so the size has to be right at launch.
type rdpDisplay struct {
	full, multimon bool
	w, h           string
}

func parseDisplay(full, multimon bool, size string) (rdpDisplay, error) {
	d := rdpDisplay{full: full, multimon: multimon}
	if size != "" {
		w, h, ok := strings.Cut(size, "x")
		_, werr := strconv.Atoi(w)
		_, herr := strconv.Atoi(h)
		if !ok || werr != nil || herr != nil {
			return d, fmt.Errorf("--size wants WIDTHxHEIGHT, e.g. 1920x1080, not %q", size)
		}
		d.w, d.h = w, h
	}
	return d, nil
}

func (d rdpDisplay) mstscArgs() []string {
	switch {
	case d.multimon:
		return []string{"/multimon"}
	case d.full:
		return []string{"/f"}
	case d.w != "":
		return []string{"/w:" + d.w, "/h:" + d.h}
	}
	return nil
}

// freerdpArgs spells the same choice for FreeRDP, which can renegotiate, so a
// plain window follows its size instead of scaling.
func (d rdpDisplay) freerdpArgs() []string {
	switch {
	case d.multimon:
		return []string{"/multimon", "/f"}
	case d.full:
		return []string{"/f"}
	case d.w != "":
		return []string{"/size:" + d.w + "x" + d.h}
	}
	return []string{"/dynamic-resolution"}
}

var rdpCmd = &cobra.Command{
	Use:   "rdp <env>",
	Short: "Enable RDP in a Windows env and open a logged-in session",
	Long: `Turns on the Windows guest's own Remote Desktop server, forwards a host port
to it and opens a client already logged in: mstsc on Windows, FreeRDP
(xfreerdp3, xfreerdp or sdl-freerdp3) elsewhere. Needs the env's SSH
credentials to do the turning on.

The session resolution is fixed at connect time: pick it with --fullscreen,
--multimon (every monitor) or --size WxH. For a windowed mstsc session that
scales when dragged, toggle "Smart sizing" in its system menu; a FreeRDP
window follows its own size.

--no-launch writes an .rdp file and prints the port instead, for other clients.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		display, err := parseDisplay(rdpFull, rdpMultimon, rdpSize)
		if err != nil {
			return err
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		port, err := e.EnableRDP(args[0], func(msg string) { fmt.Println(msg) })
		if err != nil {
			return err
		}
		path, err := e.WriteRDPFile(args[0], port)
		if err != nil {
			return err
		}
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		fmt.Printf("RDP is listening on %s\n", addr)

		if rdpNoLaunch {
			fmt.Printf("rdp file: %s\n", path)
			return nil
		}
		if runtime.GOOS == "windows" {
			return launchMstsc(e, args[0], port, addr, path, display)
		}
		return launchFreeRDP(e, args[0], addr, path, display)
	},
}

func launchMstsc(e *core.Engine, env string, port int, addr, path string, display rdpDisplay) error {
	// Anything that degrades here costs a prompt, not the connection.
	if err := e.PrepareRDPLogin(env, port); err != nil {
		fmt.Printf("note: %v\n", err)
	}
	// Launched with /v: rather than the .rdp file. Since April 2026,
	// opening an .rdp file makes mstsc show security dialogs that cannot
	// be suppressed unless the file is signed; connections started this
	// way are exempt.
	//
	// Not waited on: mstsc owns the user's attention until they close it.
	if err := exec.Command("mstsc", append([]string{"/v:" + addr}, display.mstscArgs()...)...).Start(); err != nil {
		fmt.Printf("rdp file: %s (could not launch mstsc: %v)\n", path, err)
		return nil
	}
	if _, user, _, _, err := e.SSHTarget(env); err == nil {
		fmt.Printf("opening mstsc (logged in as %s)\n", user)
	} else {
		fmt.Println("opening mstsc")
	}
	return nil
}

// freerdpClients are FreeRDP's front ends in order of preference: version 3
// packages rename the binary, version 2 is what older distributions ship.
var freerdpClients = []string{"xfreerdp3", "xfreerdp", "sdl-freerdp3"}

func findFreeRDP(lookPath func(string) (string, error)) string {
	for _, name := range freerdpClients {
		if p, err := lookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// freerdpCommandArgs builds the argument list. The password never goes on the
// command line, where any local user could read it from the process table:
// /from-stdin makes FreeRDP read it from stdin instead. /cert:ignore for the
// same reason the .rdp file sets `authentication level:i:0`: the certificate
// is one the throwaway guest made itself, reached over a loopback forward.
func freerdpCommandArgs(addr, user string, withPassword bool, display rdpDisplay) []string {
	args := []string{"/v:" + addr, "/cert:ignore"}
	if user != "" {
		args = append(args, "/u:"+user)
	}
	if withPassword {
		args = append(args, "/from-stdin:force")
	}
	return append(args, display.freerdpArgs()...)
}

// hasDisplay reports whether a graphical client has anywhere to draw.
func hasDisplay(getenv func(string) string) bool {
	return getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
}

func launchFreeRDP(e *core.Engine, env, addr, path string, display rdpDisplay) error {
	bin := findFreeRDP(exec.LookPath)
	if bin == "" || !hasDisplay(os.Getenv) {
		why := "no graphical session"
		if bin == "" {
			why = "FreeRDP is not installed (xfreerdp3 or xfreerdp)"
		}
		fmt.Printf("rdp file: %s (not opening a client: %s)\n", path, why)
		return nil
	}
	_, user, password, _, err := e.SSHTarget(env)
	if err != nil {
		return err
	}
	c := exec.Command(bin, freerdpCommandArgs(addr, user, password != "", display)...)
	if password == "" {
		// Nothing to feed it, so FreeRDP asks on this terminal: stay attached
		// until the session ends.
		fmt.Println("no password recorded for this golden, so FreeRDP will ask for one")
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
	stdin, err := c.StdinPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		fmt.Printf("rdp file: %s (could not launch %s: %v)\n", path, bin, err)
		return nil
	}
	// A pipe buffers far more than one password, so this returns at once;
	// closing it tells FreeRDP the input is complete.
	fmt.Fprintln(stdin, password)
	stdin.Close()
	// Not waited on, like mstsc: the session window belongs to the user now.
	fmt.Printf("opening %s (logged in as %s)\n", bin, user)
	return nil
}

func init() {
	rdpCmd.Flags().BoolVar(&rdpNoLaunch, "no-launch", false,
		"write the .rdp file and print the port, don't open a client")
	rdpCmd.Flags().BoolVar(&rdpFull, "fullscreen", false,
		"full screen on the current monitor")
	rdpCmd.Flags().BoolVar(&rdpMultimon, "multimon", false,
		"full screen across every monitor")
	rdpCmd.Flags().StringVar(&rdpSize, "size", "",
		"session resolution as WIDTHxHEIGHT, e.g. 1920x1080")
	rdpCmd.MarkFlagsMutuallyExclusive("fullscreen", "multimon", "size")
	rootCmd.AddCommand(rdpCmd)
}
