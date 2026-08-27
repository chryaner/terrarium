package cli

import (
	"fmt"
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

// displayArgs turns the display flags into mstsc switches. The session
// resolution is negotiated once at connect time - a window resized afterwards
// scrolls or scales, it does not renegotiate - so the size has to be right at
// launch.
func displayArgs() ([]string, error) {
	switch {
	case rdpMultimon:
		return []string{"/multimon"}, nil
	case rdpFull:
		return []string{"/f"}, nil
	case rdpSize != "":
		w, h, ok := strings.Cut(rdpSize, "x")
		if _, err := strconv.Atoi(w); ok && err == nil {
			if _, err := strconv.Atoi(h); err == nil {
				return []string{"/w:" + w, "/h:" + h}, nil
			}
		}
		return nil, fmt.Errorf("--size wants WIDTHxHEIGHT, e.g. 1920x1080, not %q", rdpSize)
	}
	return nil, nil
}

var rdpCmd = &cobra.Command{
	Use:   "rdp <env>",
	Short: "Enable RDP in a Windows env and open a logged-in session",
	Long: `Turns on the Windows guest's own Remote Desktop server, forwards a host port
to it and opens mstsc already logged in. Needs the env's SSH credentials to do
the turning on.

The session resolution is fixed at connect time: pick it with --fullscreen,
--multimon (every monitor) or --size WxH. For a windowed session that scales
when dragged, toggle "Smart sizing" in the mstsc window's system menu.

--no-launch writes an .rdp file and prints the port instead, for other clients.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		display, err := displayArgs()
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

		if rdpNoLaunch || runtime.GOOS != "windows" {
			fmt.Printf("rdp file: %s\n", path)
			return nil
		}

		// Anything that degrades here costs a prompt, not the connection.
		if err := e.PrepareRDPLogin(args[0], port); err != nil {
			fmt.Printf("note: %v\n", err)
		}
		// Launched with /v: rather than the .rdp file. Since April 2026,
		// opening an .rdp file makes mstsc show security dialogs that cannot
		// be suppressed unless the file is signed; connections started this
		// way are exempt.
		//
		// Not waited on: mstsc owns the user's attention until they close it.
		if err := exec.Command("mstsc", append([]string{"/v:" + addr}, display...)...).Start(); err != nil {
			fmt.Printf("rdp file: %s (could not launch mstsc: %v)\n", path, err)
			return nil
		}
		if _, user, _, _, err := e.SSHTarget(args[0]); err == nil {
			fmt.Printf("opening mstsc (logged in as %s)\n", user)
		} else {
			fmt.Println("opening mstsc")
		}
		return nil
	},
}

func init() {
	rdpCmd.Flags().BoolVar(&rdpNoLaunch, "no-launch", false,
		"write the .rdp file and print the port, don't open mstsc")
	rdpCmd.Flags().BoolVar(&rdpFull, "fullscreen", false,
		"full screen on the current monitor")
	rdpCmd.Flags().BoolVar(&rdpMultimon, "multimon", false,
		"full screen across every monitor")
	rdpCmd.Flags().StringVar(&rdpSize, "size", "",
		"session resolution as WIDTHxHEIGHT, e.g. 1920x1080")
	rdpCmd.MarkFlagsMutuallyExclusive("fullscreen", "multimon", "size")
	rootCmd.AddCommand(rdpCmd)
}
