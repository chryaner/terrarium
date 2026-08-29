package core

import (
	"strings"
	"testing"
)

// setupTail is what a user reads when a setup command fails, so it must keep
// the end of the output (where the error is) and drop the head, and it must
// trim surrounding whitespace so the message starts on the actual text.
func TestSetupTailKeepsShortOutputWhole(t *testing.T) {
	got := setupTail("  E: Unable to locate package cowsay\n")
	want := "E: Unable to locate package cowsay"
	if got != want {
		t.Errorf("short output should pass through trimmed: got %q, want %q", got, want)
	}
}

// The boundary: output exactly at the limit is small enough to keep whole, so
// it must not gain a "..." marker.
func TestSetupTailAtBoundaryIsNotTruncated(t *testing.T) {
	s := strings.Repeat("x", setupTailBytes)
	got := setupTail(s)
	if got != s {
		t.Errorf("output exactly at the limit must not be truncated: got %d bytes, want %d", len(got), len(s))
	}
	if strings.HasPrefix(got, "...") {
		t.Error("output at the limit should not be marked truncated")
	}
}

func TestSetupTailTruncatesToTail(t *testing.T) {
	head := strings.Repeat("A", 100)                // the boring transcript
	tail := strings.Repeat("B", setupTailBytes+500) // where the failure is
	got := setupTail(head + tail)

	if !strings.HasPrefix(got, "...") {
		t.Error("truncated output should be marked with a leading ...")
	}
	if len(got) != 3+setupTailBytes {
		t.Errorf("kept %d bytes, want %d (... + %d)", len(got), 3+setupTailBytes, setupTailBytes)
	}
	if strings.Contains(got, "A") {
		t.Error("the head of a long transcript should be dropped, keeping the tail")
	}
}
