package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runArgs exercises argument validation only. RunE reaches VirtualBox, which
// tests must not, so a copy of the command with a stubbed RunE is hung off a
// throwaway root - the real root would print its own help instead of running
// the command under test.
func runArgs(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	stub := *cmd
	stub.RunE = func(*cobra.Command, []string) error { return nil }

	root := &cobra.Command{Use: "terrarium", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(&stub)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{stub.Name()}, args...))
	return root.Execute()
}

func TestInfoTakesExactlyOneName(t *testing.T) {
	if err := runArgs(t, infoCmd, "win10"); err != nil {
		t.Errorf("one name should be accepted: %v", err)
	}
	if err := runArgs(t, infoCmd); err == nil {
		t.Error("info with no name should be rejected")
	}
	if err := runArgs(t, infoCmd, "win10", "debian-12"); err == nil {
		t.Error("info with two names should be rejected")
	}
}

// The point of the command is the architecture trap, so the help has to say so
// where someone reaching for `ls` will read it.
func TestInfoHelpMentionsArchitecture(t *testing.T) {
	if !strings.Contains(infoCmd.Long, "architecture") {
		t.Errorf("info help should explain what it is for:\n%s", infoCmd.Long)
	}
}
