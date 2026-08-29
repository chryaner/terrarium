package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/chryaner/terrarium/internal/recipe"
)

// Zero means "no opinion". A Windows install needs more than a cloud image
// boot, but an explicit request has to survive even when it equals a default.
func TestResolveHardware(t *testing.T) {
	cases := []struct {
		name       string
		format     string
		cpus, mem  int
		wantCPUs   int
		wantMemory int
	}{
		{"cloud image defaults", recipe.FormatOVA, 0, 0, DefaultCPUs, DefaultMemoryMB},
		{"qcow2 defaults", recipe.FormatQCOW2, 0, 0, DefaultCPUs, DefaultMemoryMB},
		{"iso defaults higher", recipe.FormatISO, 0, 0, isoCPUs, isoMemoryMB},
		{"explicit wins on iso", recipe.FormatISO, 8, 16384, 8, 16384},
		// The bug this guards: --cpus 2 used to be indistinguishable from
		// unset and got silently raised to 4.
		{"explicit 2 on iso is not a default", recipe.FormatISO, DefaultCPUs, DefaultMemoryMB, DefaultCPUs, DefaultMemoryMB},
		{"explicit wins on ova", recipe.FormatOVA, 1, 512, 1, 512},
		{"one set, one not", recipe.FormatISO, 6, 0, 6, isoMemoryMB},
	}
	for _, c := range cases {
		gotCPUs, gotMem := resolveHardware(c.format, c.cpus, c.mem)
		if gotCPUs != c.wantCPUs || gotMem != c.wantMemory {
			t.Errorf("%s: got %d/%d, want %d/%d", c.name, gotCPUs, gotMem, c.wantCPUs, c.wantMemory)
		}
	}
}

// A local recipe pointing an image at a private mirror must not reuse the
// upstream download it is meant to replace.
func TestCacheNameVariesByURL(t *testing.T) {
	upstream := recipe.Recipe{URL: "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.ova", Format: "ova"}
	mirror := recipe.Recipe{URL: "https://mirror.internal/noble.ova", Format: "ova"}

	a := cacheName("ubuntu-24.04", upstream)
	if b := cacheName("ubuntu-24.04", mirror); a == b {
		t.Errorf("different URLs produced the same cache file: %s", a)
	}
	if !strings.HasPrefix(a, "ubuntu-24.04-") || !strings.HasSuffix(a, ".ova") {
		t.Errorf("cache name should stay recognisable, got %s", a)
	}
	// Stable across runs, or nothing would ever be a cache hit.
	if cacheName("ubuntu-24.04", upstream) != a {
		t.Error("cache name is not deterministic")
	}
}

func TestVerifyCached(t *testing.T) {
	path, sum := testImage(t)

	if err := verifyCached(path, sum); err != nil {
		t.Errorf("matching digest should verify: %v", err)
	}
	if err := verifyCached(path, ""); err != nil {
		t.Errorf("an unpinned recipe has nothing to check: %v", err)
	}
	if err := verifyCached(path, strings.Repeat("0", 64)); err == nil {
		t.Error("a stale cached file should be rejected")
	}
	if err := verifyCached(filepath.Join(t.TempDir(), "missing"), sum); err == nil {
		t.Error("a missing file should be an error")
	}
}

// The hash is checked against a local file: downloading a real image in a unit
// test is not on.
func testImage(t *testing.T) (path, sum string) {
	t.Helper()
	body := bytes.Repeat([]byte("terrarium"), 5000)
	path = filepath.Join(t.TempDir(), "image.qcow2")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	return path, hex.EncodeToString(h[:])
}

func copyFile(t *testing.T, src, wantSHA string) (string, error) {
	t.Helper()
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = copyVerified(&out, f, info.Size(), wantSHA, func(string) {})
	return out.String(), err
}

func TestCopyVerifiedMatchingDigest(t *testing.T) {
	path, sum := testImage(t)

	got, err := copyFile(t, path, sum)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Error("copied bytes do not match the source")
	}
}

func TestCopyVerifiedUppercaseDigest(t *testing.T) {
	path, sum := testImage(t)

	// Vendors publish digests in both cases.
	if _, err := copyFile(t, path, strings.ToUpper(sum)); err != nil {
		t.Errorf("uppercase digest should verify: %v", err)
	}
}

func TestCopyVerifiedMismatch(t *testing.T) {
	path, sum := testImage(t)
	wrong := strings.Repeat("0", len(sum))

	_, err := copyFile(t, path, wrong)
	if err == nil {
		t.Fatal("expected a mismatch error")
	}
	// Both values, so the user can tell a corrupt download from a stale pin.
	if !strings.Contains(err.Error(), wrong) || !strings.Contains(err.Error(), sum) {
		t.Errorf("error should report want and got, produced: %v", err)
	}
}

func TestCopyVerifiedUnpinned(t *testing.T) {
	path, _ := testImage(t)

	// No pin, no check: vendors publishing a "latest" URL cannot pin a digest.
	if _, err := copyFile(t, path, ""); err != nil {
		t.Errorf("an unpinned copy must not fail: %v", err)
	}
}

// A recipe path is resolved against the isos directory when relative, so a
// shipped `path: winxp.iso` means "the ISO the user dropped there", and the
// missing-media error points at the exact spot.
func TestMediaResolvesRelativePathAgainstIsos(t *testing.T) {
	dir := t.TempDir()
	isos := filepath.Join(dir, "isos")
	if err := os.MkdirAll(isos, 0o755); err != nil {
		t.Fatal(err)
	}
	iso := filepath.Join(isos, "winxp.iso")
	if err := os.WriteFile(iso, []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := &Engine{}
	got, err := e.media(recipe.Recipe{Path: "winxp.iso", Format: recipe.FormatISO}, "winxp", dir, func(string) {})
	if err != nil {
		t.Fatalf("a present ISO should resolve: %v", err)
	}
	if got != iso {
		t.Errorf("got %q, want %q", got, iso)
	}
}

func TestMediaMissingRelativePathNamesTheSpot(t *testing.T) {
	dir := t.TempDir()
	_, err := (&Engine{}).media(recipe.Recipe{Path: "winxp.iso", Format: recipe.FormatISO}, "winxp", dir, func(string) {})
	if err == nil {
		t.Fatal("a missing ISO must be an error")
	}
	want := filepath.Join(dir, "isos", "winxp.iso")
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error should name the drop path %q, got %v", want, err)
	}
	if !strings.Contains(err.Error(), "terrarium get winxp") {
		t.Errorf("error should name the retry command, got %v", err)
	}
}

// An absolute path is used where it lies, with no isos-folder rewriting.
func TestMediaAbsolutePathUsedAsIs(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(t.TempDir(), "elsewhere.iso")
	if err := os.WriteFile(iso, []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := (&Engine{}).media(recipe.Recipe{Path: iso, Format: recipe.FormatISO}, "win", dir, func(string) {})
	if err != nil {
		t.Fatalf("an absolute path that exists should resolve: %v", err)
	}
	if got != iso {
		t.Errorf("got %q, want %q", got, iso)
	}
}

func TestProgressReported(t *testing.T) {
	path, _ := testImage(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var lines []string
	// Force a report on every read rather than every 64 MB.
	var out bytes.Buffer
	r := &progressReader{r: f, total: 45000, next: 1, progress: func(s string) { lines = append(lines, s) }}
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("expected progress output")
	}
	if !strings.Contains(lines[0], "/") {
		t.Errorf("progress should show total when known, got %q", lines[0])
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// The idle timer is a deadline on making *no* progress, not on total duration:
// a steady transfer, however slow per read, must pass through untouched and
// never close the body. OneByteReader forces many reads so the per-read Reset
// is exercised too.
func TestStallReaderPassesActiveTransfer(t *testing.T) {
	const payload = "the quick brown fox jumps over the lazy dog"
	var closed atomic.Bool
	sr := &stallReader{
		r:     iotest.OneByteReader(strings.NewReader(payload)),
		idle:  10 * time.Second, // never fires: every read has data ready
		close: func() error { closed.Store(true); return nil },
	}

	got, err := io.ReadAll(sr)
	if err != nil {
		t.Fatalf("a steady transfer must not error: %v", err)
	}
	if string(got) != payload {
		t.Errorf("got %q, want %q", got, payload)
	}
	if closed.Load() {
		t.Error("close must not be called on a healthy transfer")
	}
}

// A CDN that accepts the connection then goes silent would hang get forever.
// After idle with no progress, stallReader closes the body (which unblocks the
// parked read) and fails with a giving-up error. The safety timeout keeps a
// broken stallReader from hanging this test instead of failing it.
func TestStallReaderAbortsOnIdle(t *testing.T) {
	unblock := make(chan struct{})
	r := readerFunc(func([]byte) (int, error) {
		select {
		case <-unblock:
			return 0, errors.New("body closed")
		case <-time.After(2 * time.Second):
			return 0, errors.New("SAFETY-TIMEOUT")
		}
	})
	var closes atomic.Int32
	sr := &stallReader{
		r:     r,
		idle:  20 * time.Millisecond,
		close: func() error { closes.Add(1); close(unblock); return nil },
	}

	_, err := sr.Read(make([]byte, 8))
	if err == nil {
		t.Fatal("a stalled read must return an error")
	}
	if strings.Contains(err.Error(), "SAFETY-TIMEOUT") {
		t.Fatal("stallReader never tripped its idle timer")
	}
	if !strings.Contains(err.Error(), "giving up") {
		t.Errorf("want the giving-up error, got %v", err)
	}
	if n := closes.Load(); n != 1 {
		t.Errorf("body should be closed exactly once, was closed %d times", n)
	}
}
