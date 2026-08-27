package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/chryaner/terrarium/internal/keys"
	"github.com/chryaner/terrarium/internal/vbox"
	"github.com/spf13/cobra"
)

var screenshotCmd = &cobra.Command{
	Use:   "screenshot <env> [file]",
	Short: "Capture the guest's screen to a PNG",
	Long: `Captures whatever is on the guest's screen. Works on any running env,
including one with no guest additions, no network and no SSH.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0] + ".png"
		if len(args) == 2 {
			path = args[1]
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if err := e.Screenshot(args[0], abs); err != nil {
			return err
		}
		fmt.Println(abs)
		return nil
	},
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <env> <name>",
	Short: "Take a named snapshot of an env",
	Long: `Takes a named snapshot. A running env has its RAM captured too, so
restoring resumes in seconds instead of rebooting.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if err := e.Snapshot(args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("snapshot %q taken (`terrarium restore %s %s` to come back)\n",
			args[1], args[0], args[1])
		return nil
	},
}

var restoreCmd = &cobra.Command{
	Use:   "restore <env> <name>",
	Short: "Rewind an env to a named snapshot",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if err := e.RestoreSnap(args[0], args[1], func(msg string) { fmt.Println(msg) }); err != nil {
			return err
		}
		fmt.Printf("%s is back at %q\n", args[0], args[1])
		return nil
	},
}

var showCmd = &cobra.Command{
	Use:   "show <env>",
	Short: "Open a GUI window onto an env",
	Long: `Starts the env with a VirtualBox window attached, for watching an install
or using a desktop. The window can be closed without stopping the env.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		if err := e.Show(args[0]); err != nil {
			return err
		}
		fmt.Printf("window attached to %s (closing it leaves the env running)\n", args[0])
		return nil
	},
}

var keysCmd = &cobra.Command{
	Use:   "keys <env> <key...>",
	Short: "Press keys in the guest",
	Long: `Sends key presses to the guest's keyboard. Chords land as chords.

Valid keys: ` + strings.Join(keys.Names(), ", "),
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		return e.PressKeys(args[0], args[1:])
	},
}

var typeEnter bool

var typeCmd = &cobra.Command{
	Use:   "type <env> <text>",
	Short: "Type text into the guest",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		return e.TypeText(args[0], args[1], typeEnter)
	},
}

// coord parses one screen coordinate, named so the error says which one.
func coord(name, s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative pixel coordinate, got %q", name, s)
	}
	return n, nil
}

var clickRight, clickMiddle, clickDouble bool

var clickCmd = &cobra.Command{
	Use:   "click <env> <x> <y>",
	Short: "Click the guest's screen",
	Long: `Presses and releases a mouse button at a screen position, in the same
0-based pixel coordinates a screenshot shows. Like screenshot and keys, it
goes through the hypervisor and needs nothing of the guest.`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		x, err := coord("x", args[1])
		if err != nil {
			return err
		}
		y, err := coord("y", args[2])
		if err != nil {
			return err
		}
		button := vbox.MouseLeft
		if clickRight {
			button = vbox.MouseRight
		}
		if clickMiddle {
			button = vbox.MouseMiddle
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		return e.Click(args[0], x, y, button, clickDouble)
	},
}

var moveCmd = &cobra.Command{
	Use:   "move <env> <x> <y>",
	Short: "Move the guest's mouse pointer",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		x, err := coord("x", args[1])
		if err != nil {
			return err
		}
		y, err := coord("y", args[2])
		if err != nil {
			return err
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		return e.MoveMouse(args[0], x, y)
	},
}

var scrollCmd = &cobra.Command{
	Use:   "scroll <env> <clicks>",
	Short: "Turn the guest's mouse wheel",
	Long: `Turns the wheel under wherever the pointer is: positive scrolls down,
negative scrolls up. Use move first to aim it at a window.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clicks, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("clicks must be an integer, got %q", args[1])
		}
		e, err := core.NewEngine()
		if err != nil {
			return err
		}
		return e.Scroll(args[0], clicks)
	},
}

func init() {
	typeCmd.Flags().BoolVar(&typeEnter, "enter", false, "press enter after the text")
	clickCmd.Flags().BoolVar(&clickRight, "right", false, "right button")
	clickCmd.Flags().BoolVar(&clickMiddle, "middle", false, "middle button")
	clickCmd.Flags().BoolVar(&clickDouble, "double", false, "double-click")
	rootCmd.AddCommand(screenshotCmd, snapshotCmd, restoreCmd, showCmd, keysCmd, typeCmd,
		clickCmd, moveCmd, scrollCmd)
}
