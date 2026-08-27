package main

import (
	"image"
	"testing"
)

func TestTimer(t *testing.T) {
	cases := map[float64]string{0: "0:00", 4.4: "0:04", 40.9: "0:41", 65: "1:05"}
	for in, want := range cases {
		if got := timer(in); got != want {
			t.Errorf("timer(%v) = %q, want %q", in, got, want)
		}
	}
}

// Guests change resolution mid-boot; every shot must land on the same canvas.
func TestFitToLetterboxes(t *testing.T) {
	for _, srcW := range []int{640, 1024} {
		src := image.NewRGBA(image.Rect(0, 0, srcW, srcW*3/4))
		dst := fitTo(src, 820, 615)
		if dst.Bounds().Dx() != 820 || dst.Bounds().Dy() != 615 {
			t.Errorf("%d-wide shot: box = %v, want 820x615", srcW, dst.Bounds())
		}
	}
	// A wider-than-box aspect pads top and bottom rather than distorting.
	wide := image.NewRGBA(image.Rect(0, 0, 1600, 400))
	dst := fitTo(wide, 820, 615)
	if dst.Bounds().Dx() != 820 || dst.Bounds().Dy() != 615 {
		t.Errorf("wide shot: box = %v, want 820x615", dst.Bounds())
	}
}

func TestTrunc(t *testing.T) {
	if got := trunc("hello", 10); got != "hello" {
		t.Errorf("short string changed: %q", got)
	}
	if got := trunc("hello world", 5); got != "hell…" {
		t.Errorf("trunc = %q, want %q", got, "hell…")
	}
}

func TestSplitLines(t *testing.T) {
	// A real newline and an escaped one both split, so a caption reads the same
	// whether the JSON kept "\n" literal or not.
	for _, in := range []string{"a\nb", `a\nb`} {
		got := splitLines(in)
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("splitLines(%q) = %q", in, got)
		}
	}
	if got := splitLines("one line"); len(got) != 1 {
		t.Errorf("splitLines with no break = %q", got)
	}
}

// A short command gets the big card face; a long one steps down instead of
// overflowing the frame.
func TestFitSize(t *testing.T) {
	r, err := newRenderer(820)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.fitSize("terrarium fork win10 demo"); got != cardFontSize {
		t.Errorf("short command should use the card face, got %d", got)
	}
	long := "terrarium type agent \"the taskbar clock read 1:36 PM when I started\" --enter"
	if got := r.fitSize(long); got >= cardFontSize {
		t.Errorf("long command should step down from %d, got %d", cardFontSize, got)
	}
}
