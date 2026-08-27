// vmgif builds a README demo from real VM screenshots, structured like a
// film rather than a terminal session. Each command gets a full-frame title
// card - the command types out with nothing else on screen, so there is time
// to read it - then a hard cut to full-bleed guest footage with the command
// pinned in a bar above it. At any frame the viewer can answer "which command
// is doing this", even joining a looping GIF halfway through. A header shows
// machine state and a timer reading real wall-clock time, so a sped-up demo
// stays honest about how long things took. ffmpeg turns the frames it writes
// into the GIF.
//
// Usage: go run ./tools/vmgif <manifest.json> <out-dir>
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type manifest struct {
	Title  string  `json:"title"`
	Dir    string  `json:"dir"`   // where the screenshot files live
	Width  int     `json:"width"` // output width in px
	Scenes []scene `json:"scenes"`
}

// A scene is either a card (Card set) or footage (VM set), never both.
type scene struct {
	// Card is a command shown full-frame, typed out character by character,
	// with Sub as a plain-language annotation beneath it. After its card, the
	// command stays pinned above the footage until the next card replaces it.
	Card string `json:"card"`
	Sub  string `json:"sub"`
	// Hold: on footage, how long the shot stays up (default 1; a caption
	// always gets at least its reading time). On a card, overrides the
	// computed reading pause after typing.
	Hold int `json:"hold"`

	// VM is the screenshot shown full-bleed below the command bar.
	VM string `json:"vm"`
	// Cmd updates the pinned command bar without a card's ceremony, for runs
	// of small commands (a game's clicks) where a full card per move would
	// drown the footage. The bar stays honest; the caption carries the why.
	Cmd string `json:"cmd"`
	// Caption is drawn centered on the footage, for the beats that deserve
	// narration. \n splits lines.
	Caption      string  `json:"caption"`
	CaptionStyle string  `json:"captionStyle"` // ok | err | plain (default)
	T            float64 `json:"t"`            // real seconds on the timer, <0 hides it
	State        string  `json:"state"`        // booting | ready | broken | reverting
}

// Card pacing. Typing runs at one character per frame until a command is so
// long it would drag, then speeds up just enough to stay near typeMaxFrames.
const typeMaxFrames = 30

// Reading time is proportional to how much text is on screen, the way
// subtitles are timed: a floor to register at all, plus ~20 characters per
// second at the 10 fps the GIFs are assembled at.
func readFrames(floor int, texts ...string) int {
	n := 0
	for _, t := range texts {
		n += len([]rune(t))
	}
	if f := floor + n/2; f > floor {
		return f
	}
	return floor
}

// The panel face is what the command bar uses; the card face is where typing
// starts. fitSize picks between them so long commands stay on one line.
const (
	panelFontSize = 19
	cardFontSize  = 30
)

var (
	colCanvasBG = color.RGBA{0x0d, 0x11, 0x17, 0xff}
	colBarBG    = color.RGBA{0x16, 0x1b, 0x22, 0xff}
	colCmd      = color.RGBA{0x5c, 0xe0, 0x87, 0xff}
	colOK       = color.RGBA{0x5c, 0xe0, 0x87, 0xff}
	colErr      = color.RGBA{0xf8, 0x51, 0x49, 0xff}
	colText     = color.RGBA{0xf0, 0xf3, 0xf6, 0xff}
	colDim      = color.RGBA{0x8b, 0x94, 0x9e, 0xff}
)

func captionColor(style string) color.RGBA {
	switch style {
	case "ok":
		return colOK
	case "err":
		return colErr
	default:
		return colText
	}
}

// stateColor and stateLabel give the header's right-hand status its color and
// word.
func stateColor(s string) color.RGBA {
	switch s {
	case "ready":
		return colOK
	case "broken":
		return colErr
	default: // booting, reverting
		return color.RGBA{0xd7, 0xab, 0x53, 0xff}
	}
}

type renderer struct {
	face          font.Face
	ascent, lineH int
	advance       int
	faces         map[int]font.Face // panelFontSize..cardFontSize, for card typing
	padX          int
	barH, cmdH    int
	vmH           int
	width         int
}

func newRenderer(width int) (*renderer, error) {
	ttf, err := opentype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	faces := map[int]font.Face{}
	for size := panelFontSize; size <= cardFontSize; size++ {
		f, err := opentype.NewFace(ttf, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		faces[size] = f
	}
	face := faces[panelFontSize]
	adv, _ := face.GlyphAdvance('M')
	m := face.Metrics()
	r := &renderer{
		face:    face,
		ascent:  m.Ascent.Ceil(),
		lineH:   m.Height.Ceil(),
		advance: adv.Ceil(),
		faces:   faces,
		padX:    18,
		width:   width,
		// Guests are 4:3 and change resolution mid-boot; the canvas cannot,
		// so the footage box is fixed and shots letterbox into it.
		vmH: width * 3 / 4,
	}
	r.barH = r.lineH + 16
	r.cmdH = r.lineH + 12
	return r, nil
}

func (r *renderer) canvasH() int { return r.barH + r.cmdH + r.vmH }
func (r *renderer) vmTop() int   { return r.barH + r.cmdH }
func (r *renderer) cols() int    { return (r.width - 2*r.padX) / r.advance }

func (r *renderer) text(dst draw.Image, x, y int, s string, c color.Color, face font.Face) int {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
	return d.Dot.X.Ceil()
}

func (r *renderer) textWidth(s string) int { return font.MeasureString(r.face, s).Ceil() }

func timer(sec float64) string {
	t := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

// fitTo scales src to fit a w×h box, centered on the canvas background:
// guests change resolution mid-boot and the canvas cannot follow.
func fitTo(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(colCanvasBG), image.Point{}, draw.Src)
	b := src.Bounds()
	s := math.Min(float64(w)/float64(b.Dx()), float64(h)/float64(b.Dy()))
	dw, dh := int(float64(b.Dx())*s+0.5), int(float64(b.Dy())*s+0.5)
	x, y := (w-dw)/2, (h-dh)/2
	xdraw.CatmullRom.Scale(dst, image.Rect(x, y, x+dw, y+dh), src, b, xdraw.Over, nil)
	return dst
}

// trunc cuts a string to at most n runes so a long line cannot overflow.
func trunc(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	if n <= 1 {
		return string(rs[:n])
	}
	return string(rs[:n-1]) + "…"
}

// fitSize picks the biggest card face the command fits the frame width at.
func (r *renderer) fitSize(text string) int {
	for _, size := range []int{30, 26, 23, 19} {
		if font.MeasureString(r.faces[size], text).Ceil() <= r.width-2*r.padX {
			return size
		}
	}
	return panelFontSize
}

// blendRect darkens a rectangle toward black by f (0..1), so caption text
// over a bright desktop stays legible.
func blendRect(dst *image.RGBA, rect image.Rectangle, f float64) {
	rect = rect.Intersect(dst.Bounds())
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			r, g, b, a := dst.RGBAAt(x, y).RGBA()
			m := func(v uint32) uint8 { return uint8(float64(v>>8) * (1 - f)) }
			dst.SetRGBA(x, y, color.RGBA{m(r), m(g), m(b), uint8(a >> 8)})
		}
	}
}

// splitLines splits on \n, tolerating an escaped "\n" that survived JSON.
func splitLines(s string) []string {
	out := []string{}
	cur := ""
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		if rs[i] == '\\' && i+1 < len(rs) && rs[i+1] == 'n' {
			out = append(out, cur)
			cur = ""
			i++
			continue
		}
		cur += string(rs[i])
	}
	return append(out, cur)
}

// chrome draws the header (title left, state and timer right) and the command
// bar with the pinned active command, returning a canvas ready for its body.
func (r *renderer) chrome(title, activeCmd, state string, t float64) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.width, r.canvasH()))
	draw.Draw(out, out.Bounds(), image.NewUniform(colCanvasBG), image.Point{}, draw.Src)

	draw.Draw(out, image.Rect(0, 0, r.width, r.barH), image.NewUniform(colBarBG), image.Point{}, draw.Src)
	base := (r.barH-r.lineH)/2 + r.ascent
	r.text(out, r.padX, base, title, colDim, r.face)
	status := state
	if t >= 0 {
		status = state + "  " + timer(t)
	}
	r.text(out, r.width-r.padX-r.textWidth(status), base, status, stateColor(state), r.face)

	if activeCmd != "" {
		y := r.barH + (r.cmdH-r.lineH)/2 + r.ascent
		x := r.text(out, r.padX, y, "> ", colDim, r.face)
		r.text(out, x, y, trunc(activeCmd, r.cols()-2), colCmd, r.face)
	}
	return out
}

// cardFrame is one frame of a command card: the command typed so far,
// centered in the footage area with nothing competing for attention, and the
// annotation once typing is done.
func (r *renderer) cardFrame(title, state string, t float64, full, shown string, size int, cursor bool, sub string, showSub bool) *image.RGBA {
	out := r.chrome(title, "", state, t)
	face := r.faces[size]
	m := face.Metrics()
	h, asc := m.Height.Ceil(), m.Ascent.Ceil()

	mid := r.vmTop() + r.vmH/2
	x := (r.width - font.MeasureString(face, full).Ceil()) / 2
	baseline := mid - h/2 + asc
	end := r.text(out, x, baseline, shown, colCmd, face)
	if cursor {
		cw, _ := face.GlyphAdvance('M')
		draw.Draw(out, image.Rect(end+2, baseline-asc+3, end+2+cw.Ceil(), baseline+3),
			image.NewUniform(colCmd), image.Point{}, draw.Over)
	}
	if showSub && sub != "" {
		y := baseline + h + r.lineH/2
		for _, ln := range splitLines(sub) {
			w := r.textWidth(ln)
			r.text(out, (r.width-w)/2, y+r.ascent, ln, colDim, r.face)
			y += r.lineH
		}
	}
	return out
}

// footageFrame is the guest's screen full-bleed under the pinned command,
// with an optional centered caption on the footage.
func (r *renderer) footageFrame(shot *image.RGBA, title, activeCmd, state string, t float64, caption, captionStyle string) *image.RGBA {
	out := r.chrome(title, activeCmd, state, t)
	draw.Draw(out, image.Rect(0, r.vmTop(), r.width, r.vmTop()+r.vmH), shot, image.Point{}, draw.Src)
	if caption != "" {
		r.drawCaption(out, caption, captionColor(captionStyle))
	}
	return out
}

// drawCaption centers a multi-line caption on the footage with a dark band
// behind it for contrast.
func (r *renderer) drawCaption(dst *image.RGBA, text string, col color.RGBA) {
	big := r.faces[cardFontSize]
	m := big.Metrics()
	bigH, bigAsc := m.Height.Ceil(), m.Ascent.Ceil()

	lines := splitLines(text)
	block := len(lines) * (bigH + 6)
	mid := r.vmTop() + r.vmH/2
	blendRect(dst, image.Rect(0, mid-block/2-16, r.width, mid+block/2+16), 0.62)

	y := mid - block/2 + bigAsc
	for _, ln := range lines {
		w := font.MeasureString(big, ln).Ceil()
		d := &font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: big, Dot: fixed.P((r.width-w)/2, y)}
		d.DrawString(ln)
		y += bigH + 6
	}
}

func run(manifestPath, outDir string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var mf manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return fmt.Errorf("%s: %w", manifestPath, err)
	}
	if mf.Width == 0 {
		mf.Width = 820
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	r, err := newRenderer(mf.Width)
	if err != nil {
		return err
	}

	n := 0
	emit := func(frame *image.RGBA) error {
		out, err := os.Create(filepath.Join(outDir, fmt.Sprintf("out_%04d.png", n)))
		if err != nil {
			return err
		}
		if err := png.Encode(out, frame); err != nil {
			out.Close()
			return err
		}
		n++
		return out.Close()
	}

	active := ""
	shots := map[string]*image.RGBA{}
	for i, sc := range mf.Scenes {
		switch {
		case sc.Card != "" && sc.VM != "":
			return fmt.Errorf("scene %d: card and vm are alternatives, set one", i)

		case sc.Card != "":
			text := sc.Card
			size := r.fitSize(text)
			for font.MeasureString(r.faces[size], text).Ceil() > r.width-2*r.padX {
				text = trunc(text, len([]rune(text))-2)
			}
			rs := []rune(text)
			cpf := (len(rs) + typeMaxFrames - 1) / typeMaxFrames
			for c := cpf; ; c += cpf {
				if c > len(rs) {
					c = len(rs)
				}
				if err := emit(r.cardFrame(mf.Title, sc.State, sc.T, text, string(rs[:c]), size, true, sc.Sub, false)); err != nil {
					return err
				}
				if c == len(rs) {
					break
				}
			}
			// The command was read as it typed; the annotation appears now,
			// so the pause is timed to it.
			pause := sc.Hold
			if pause == 0 {
				pause = readFrames(14, sc.Sub)
			}
			for b := 0; b < pause; b++ {
				if err := emit(r.cardFrame(mf.Title, sc.State, sc.T, text, text, size, b%4 < 2, sc.Sub, true)); err != nil {
					return err
				}
			}
			active = sc.Card

		case sc.VM != "":
			if sc.Cmd != "" {
				active = sc.Cmd
			}
			shot, ok := shots[sc.VM]
			if !ok {
				f, err := os.Open(filepath.Join(mf.Dir, sc.VM))
				if err != nil {
					return err
				}
				img, err := png.Decode(f)
				f.Close()
				if err != nil {
					return fmt.Errorf("%s: %w", sc.VM, err)
				}
				shot = fitTo(img, r.width, r.vmH)
				shots[sc.VM] = shot
			}
			frame := r.footageFrame(shot, mf.Title, active, sc.State, sc.T, sc.Caption, sc.CaptionStyle)
			hold := sc.Hold
			if sc.Caption != "" {
				if min := readFrames(12, sc.Caption); hold < min {
					hold = min
				}
			}
			if hold < 1 {
				hold = 1
			}
			for h := 0; h < hold; h++ {
				if err := emit(frame); err != nil {
					return err
				}
			}

		default:
			return fmt.Errorf("scene %d: one of card or vm is required", i)
		}
	}
	fmt.Printf("wrote %d frames to %s\n", n, outDir)
	return nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: vmgif <manifest.json> <out-dir>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
