package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chryaner/terrarium/internal/recipe"
	"github.com/chryaner/terrarium/internal/seed"
	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/chryaner/terrarium/internal/vbox"
)

const (
	goldenPrefix = VMPrefix + "golden-"
	// seedController is ours: named so it is recognisable in the VirtualBox
	// GUI and never collides with a controller the appliance shipped.
	seedController = "trrseed"
	// diskController holds the boot disk on VMs we build from a bare image.
	diskController = "SATA"
	// DefaultCPUs and DefaultMemoryMB are what `get` uses when the caller has
	// no opinion. resolveHardware raises them for formats that need more.
	DefaultCPUs     = 2
	DefaultMemoryMB = 2048
	isoCPUs         = 4
	isoMemoryMB     = 4096
	// cloud-init runs package and network setup on first boot, so this is
	// well beyond a normal boot.
	firstBootTimeout = 6 * time.Minute
	shutdownTimeout  = 3 * time.Minute
	progressBytes    = 64 << 20
	// downloadIdleTimeout is how long a transfer may produce no bytes at all
	// before it is treated as stalled.
	downloadIdleTimeout = 60 * time.Second
	// A fresh sshd can reset the first connections it accepts.
	cloudInitAttempts   = 4
	cloudInitRetryDelay = 5 * time.Second
)

// Get turns a recipe into a ready golden with no manual steps. For a cloud
// image: download, generate credentials, import, boot once so cloud-init
// applies them, shut down cleanly, snapshot. For an ISO: run the installer
// unattended. For a derived recipe: fork the base, run its setup commands,
// flatten the result.
func (e *Engine) Get(image string, cpus, memMB int, force bool, progress func(string)) (*Golden, error) {
	return e.get(image, cpus, memMB, force, 0, progress)
}

// get carries the from-chain depth, which the public entrypoint starts at 0.
func (e *Engine) get(image string, cpus, memMB int, force bool, depth int, progress func(string)) (*Golden, error) {
	r, err := recipe.Lookup(image)
	if err != nil {
		return nil, err
	}
	if r.Local {
		progress("using local recipe for " + image)
	}
	if r.From != "" {
		return e.getDerived(r, image, cpus, memMB, force, depth, progress)
	}
	dir, err := dataDir()
	if err != nil {
		return nil, err
	}
	vmName := goldenPrefix + image

	if err := e.clearExisting(image, vmName, force, progress); err != nil {
		return nil, err
	}

	imagePath, err := e.media(r, image, dir, progress)
	if err != nil {
		return nil, err
	}
	cpus, memMB = resolveHardware(r.Format, cpus, memMB)

	// Windows brings its own installer and account handling; there is no
	// cloud-init to seed.
	if r.Format == recipe.FormatISO {
		progress(fmt.Sprintf("creating %s (%d cpu, %d MB, %d GB disk)", vmName, cpus, memMB, r.DiskGB))
		return e.buildWindowsGolden(r, image, vmName, imagePath, cpus, memMB, progress)
	}

	progress("generating SSH key and cloud-init seed")
	keyPath, isoPath, err := seed.Generate(filepath.Join(dir, "goldens"), image, r.Packages)
	if err != nil {
		return nil, err
	}

	switch r.Format {
	case recipe.FormatOVA:
		progress(fmt.Sprintf("importing as %s (%d cpu, %d MB)", vmName, cpus, memMB))
		if err := e.VB.ImportOVA(imagePath, vmName, cpus, memMB); err != nil {
			return nil, err
		}
	case recipe.FormatQCOW2:
		progress(fmt.Sprintf("creating %s (%d cpu, %d MB)", vmName, cpus, memMB))
		if err := e.createFromDisk(vmName, image, imagePath, r.OSType, cpus, memMB, progress); err != nil {
			return nil, leftRegistered(vmName, err)
		}
	}

	// The VM exists from here on. A half-built golden is left registered so
	// the failure can be inspected; deleting the evidence would be worse.
	g, err := e.buildGolden(image, vmName, keyPath, isoPath, progress)
	if err != nil {
		return nil, leftRegistered(vmName, err)
	}
	return g, nil
}

// clearExisting enforces the rebuild contract every build shares: an existing
// golden VM stops `get` unless --force, and --force is refused while envs are
// linked clones of the VM's snapshot - unregistering it would take their disks
// with it. After the VM goes, so does its state record: a failed rebuild must
// not leave a golden entry pointing at nothing.
func (e *Engine) clearExisting(image, vmName string, force bool, progress func(string)) error {
	if _, err := e.findVM(vmName); err != nil {
		return nil
	}
	if !force {
		return fmt.Errorf("%s already exists: pass --force to rebuild it", vmName)
	}
	if forks := e.forksOf(image); len(forks) > 0 {
		return fmt.Errorf("%s is still forked by %s: `terrarium rm` them first",
			vmName, strings.Join(forks, ", "))
	}
	progress("removing " + vmName)
	// Best effort before unregistering: the VM is usually already off, and
	// Unregister is the call whose failure actually matters.
	e.stopForUnregister(vmName)
	if err := e.VB.Unregister(vmName); err != nil {
		return err
	}
	if e.St.Goldens[image] != nil {
		delete(e.St.Goldens, image)
		return e.St.Save()
	}
	return nil
}

// media resolves the recipe's installation media to a path on disk,
// downloading it if the recipe names a URL. A recipe with a path instead uses
// the file where it lies: Windows ISOs have no stable download, so users bring
// their own and there is no point copying gigabytes into a cache. A relative
// path resolves against the isos directory, so a shipped recipe can say
// `path: win10.iso` and mean "whatever ISO the user dropped there".
func (e *Engine) media(r recipe.Recipe, image, dir string, progress func(string)) (string, error) {
	if r.Path != "" {
		path := r.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, "isos", path)
		}
		if _, err := os.Stat(path); err != nil {
			if !filepath.IsAbs(r.Path) {
				return "", fmt.Errorf("no installation media for %s: this image cannot be redistributed, so bring your own.\ndownload the ISO, save it as %s, and run `terrarium get %s` again", image, path, image)
			}
			return "", fmt.Errorf("recipe %s: %w", image, err)
		}
		progress("using " + path)
		return path, nil
	}

	// Reused even under --force: the published image is content-stable, and
	// --force means rebuild the VM, not refetch half a gigabyte.
	path := filepath.Join(dir, "images", cacheName(image, r))
	if _, err := os.Stat(path); err == nil {
		if err := verifyCached(path, r.SHA256); err == nil {
			progress("cached " + path)
			return path, nil
		} else {
			progress("cached copy failed its checksum, downloading again: " + err.Error())
		}
	}
	progress("downloading " + r.URL)
	if err := download(r.URL, path, r.SHA256, progress); err != nil {
		return "", err
	}
	return path, nil
}

// cacheName keys the cached file by URL as well as image name, so a local
// recipe pointing an image at a private mirror does not silently reuse the
// upstream download it is meant to replace.
func cacheName(image string, r recipe.Recipe) string {
	sum := sha256.Sum256([]byte(r.URL))
	return fmt.Sprintf("%s-%s.%s", image, hex.EncodeToString(sum[:4]), r.Format)
}

// verifyCached rechecks a pinned digest before reuse. Without a pin there is
// nothing to check and the file is taken as-is.
func verifyCached(path, wantSHA256 string) error {
	if wantSHA256 == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return err
	}
	got := hex.EncodeToString(sum.Sum(nil))
	if !strings.EqualFold(got, wantSHA256) {
		return fmt.Errorf("sha256 is %s, want %s", got, strings.ToLower(wantSHA256))
	}
	return nil
}

// resolveHardware fills in whatever the caller had no opinion about, which is
// what a zero means here. Installing Windows on 2 CPUs and 2 GB is painful
// where booting a cloud image is not, so an iso build defaults higher - but an
// explicit request always wins, including an explicit 2.
func resolveHardware(format string, cpus, memMB int) (int, int) {
	defCPUs, defMemMB := DefaultCPUs, DefaultMemoryMB
	if format == recipe.FormatISO {
		defCPUs, defMemMB = isoCPUs, isoMemoryMB
	}
	if cpus <= 0 {
		cpus = defCPUs
	}
	if memMB <= 0 {
		memMB = defMemMB
	}
	return cpus, memMB
}

func leftRegistered(vmName string, err error) error {
	return fmt.Errorf("%w\n%s was left registered: inspect it, then `%s`",
		err, vmName, vbox.ManualDeleteHint(vmName))
}

// createFromDisk builds a VM around a bare cloud disk image, for the
// distributions that publish qcow2 rather than a full appliance. The
// converted VDI lives in the VM folder and dies with the VM: only the
// downloaded qcow2 is cached.
func (e *Engine) createFromDisk(vmName, image, srcPath, osType string, cpus, memMB int, progress func(string)) error {
	if err := e.VB.CreateVM(vmName, osType); err != nil {
		return err
	}
	// createvm's memory default is far too small to boot a modern guest, so
	// both values are always set here rather than left to inherit.
	if err := e.VB.ModifyCPUMem(vmName, cpus, memMB); err != nil {
		return err
	}

	folder, err := e.VB.MachineFolder()
	if err != nil {
		return err
	}
	vdiPath := filepath.Join(folder, vmName, image+".vdi")

	progress("converting " + filepath.Base(srcPath) + " to VDI")
	if err := e.VB.CloneMedium(srcPath, vdiPath); err != nil {
		return err
	}
	// clonemedium registers its source. Drop the download from the media
	// registry so it stays an ordinary cached file the next run can reuse.
	if err := e.VB.CloseMedium(srcPath); err != nil {
		return err
	}

	if err := e.VB.AddSATAController(vmName, diskController, 2); err != nil {
		return err
	}
	return e.VB.AttachHDD(vmName, diskController, 0, vdiPath)
}

func (e *Engine) forksOf(golden string) []string {
	var names []string
	for name, env := range e.St.Envs {
		if env.Golden == golden {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (e *Engine) buildGolden(image, vmName, keyPath, isoPath string, progress func(string)) (*Golden, error) {
	port, err := e.freePort()
	if err != nil {
		return nil, err
	}
	if err := e.VB.SetNATSSH(vmName, port); err != nil {
		return nil, err
	}

	seedCtrl, seedPort, err := e.attachSeed(vmName, isoPath, progress)
	if err != nil {
		return nil, err
	}

	progress("first boot: cloud-init applies the seed")
	if err := e.VB.StartHeadless(vmName); err != nil {
		return nil, err
	}
	if err := sshx.WaitReady(port, firstBootTimeout); err != nil {
		return nil, err
	}

	code, out, err := waitCloudInit(port, keyPath)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.Contains(out, "status: error"):
		return nil, fmt.Errorf("cloud-init failed (exit %d):\n%s", code, strings.TrimSpace(out))
	case code == 2 || strings.Contains(out, "degraded"):
		// A non-critical module failed. The image is still usable, and this
		// is common enough on stock cloud images to be worth continuing.
		progress("warning: cloud-init finished degraded: " + lastLine(out))
	case code != 0:
		return nil, fmt.Errorf("cloud-init did not finish cleanly (exit %d):\n%s", code, strings.TrimSpace(out))
	}

	if err := e.shutdownGuest(vmName, port, seed.User, "", keyPath, "sudo shutdown -h now", progress); err != nil {
		return nil, err
	}

	// The seed is single-use: leaving it attached would re-seed every fork.
	if err := e.VB.DetachDVD(vmName, seedCtrl, seedPort, 0); err != nil {
		return nil, err
	}

	return e.recordGolden(image, &Golden{
		VMName:  vmName,
		SSHUser: seed.User,
		SSHKey:  keyPath,
	}, progress)
}

// shutdownGuest asks the guest to power itself off, and pulls the plug if it
// will not. ACPI is the fallback rather than the first move so the guest gets
// a chance to flush its disks.
func (e *Engine) shutdownGuest(vmName string, port int, user, password, key, command string, progress func(string)) error {
	progress("shutting down")
	// The connection drops as the guest goes down; that is not a failure.
	sshx.ExecStreams(port, user, password, key, command, io.Discard, io.Discard)
	if err := e.VB.WaitOff(vmName, shutdownTimeout); err != nil {
		e.VB.AcpiPowerButton(vmName)
		if err := e.VB.WaitOff(vmName, time.Minute); err != nil {
			return err
		}
	}
	return nil
}

// recordGolden snapshots the finished VM and writes it into state. Callers
// fill in the credentials; the rest is the same whatever the format was.
func (e *Engine) recordGolden(image string, g *Golden, progress func(string)) (*Golden, error) {
	progress("snapshotting " + DefaultSnapshot)
	if err := e.VB.TakeSnapshot(g.VMName, DefaultSnapshot); err != nil {
		return nil, err
	}
	g.Snapshot = DefaultSnapshot
	g.Owned = true
	if vm, err := e.findVM(g.VMName); err == nil {
		g.UUID = vm.UUID
	}
	e.St.Goldens[image] = g
	return g, e.St.Save()
}

// waitCloudInit blocks until cloud-init has finished, returning its exit code
// and combined output. sshd answers its banner before it will reliably
// authenticate, so a failed connection is retried; a non-zero exit is the
// guest's answer and is not.
func waitCloudInit(port int, keyPath string) (int, string, error) {
	var err error
	for attempt := 0; attempt < cloudInitAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(cloudInitRetryDelay)
		}
		var out sshx.OutputBuffer
		var code int
		code, err = sshx.ExecStreams(port, seed.User, "", keyPath,
			"cloud-init status --wait", &out, &out)
		if err == nil {
			return code, out.String(), nil
		}
	}
	return 0, "", err
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// attachSeed puts the seed ISO on a controller of our own rather than one the
// appliance shipped: the Ubuntu OVA's PIIX4 IDE controller hangs the noble
// kernel dead during the ATAPI probe, while AHCI probes it cleanly. It also
// drops the appliance's phantom floppy controller, which the guest reads and
// retries forever ("I/O error, dev fd0"). Measured cost of the floppy was ~2s
// of boot, not the ~200s first-boot stall - that one is first-boot-only work
// and covered by firstBootTimeout.
func (e *Engine) attachSeed(vm, isoPath string, progress func(string)) (ctrl string, port int, err error) {
	ctrls, err := e.VB.StorageControllers(vm)
	if err != nil {
		return "", 0, err
	}

	var haveSeedCtrl bool
	sata := ""
	for _, c := range ctrls {
		switch {
		case c.Name == seedController:
			haveSeedCtrl = true
		case strings.EqualFold(c.Bus, "SATA"):
			sata = c.Name
		case strings.EqualFold(c.Bus, "Floppy"):
			progress("removing floppy controller")
			if err := e.VB.RemoveStorageController(vm, c.Name); err != nil {
				return "", 0, err
			}
		}
	}

	// VirtualBox allows one SATA controller per VM. When the boot disk
	// already lives on it (qcow2 builds), the seed shares it on port 1;
	// otherwise (OVA builds, disk on SCSI) a dedicated controller is added.
	switch {
	case haveSeedCtrl:
		return seedController, 0, e.VB.AttachDVD(vm, seedController, 0, isoPath)
	case sata != "":
		return sata, 1, e.VB.AttachDVD(vm, sata, 1, isoPath)
	default:
		if err := e.VB.AddSATAController(vm, seedController, 1); err != nil {
			return "", 0, err
		}
		return seedController, 0, e.VB.AttachDVD(vm, seedController, 0, isoPath)
	}
}

// download streams to a .partial file first: an interrupted or corrupt
// transfer must never be mistaken for a cached image on the next run.
func download(url, dst, wantSHA256 string, progress func(string)) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// No overall timeout: a 6 GB ISO legitimately takes a long time. The
	// deadline is on making progress, applied per read below.
	client := &http.Client{Timeout: 0}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	tmp := dst + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	body := &stallReader{r: resp.Body, idle: downloadIdleTimeout, close: resp.Body.Close}
	err = copyVerified(f, body, resp.ContentLength, wantSHA256, progress)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s: %w", url, err)
	}
	return os.Rename(tmp, dst)
}

// stallReader fails a download that stops making progress. A whole-request
// timeout is wrong here - a multi-gigabyte image legitimately takes minutes  -
// but a CDN that accepts the connection and then goes quiet would otherwise
// hang `get` forever, against the driver's every-call-has-a-deadline rule.
type stallReader struct {
	r     io.Reader
	idle  time.Duration
	close func() error

	timer *time.Timer
	dead  atomic.Bool
}

func (s *stallReader) Read(p []byte) (int, error) {
	if s.timer == nil {
		// Closing the body is what unblocks a Read parked in the kernel;
		// there is no deadline to set on an http response body.
		s.timer = time.AfterFunc(s.idle, func() {
			s.dead.Store(true)
			s.close()
		})
	} else {
		s.timer.Reset(s.idle)
	}

	n, err := s.r.Read(p)
	if err != nil && s.dead.Load() {
		s.timer.Stop()
		return n, fmt.Errorf("no data for %s, giving up", s.idle)
	}
	if err != nil {
		s.timer.Stop()
	}
	return n, err
}

// copyVerified streams src to dst, reporting progress and, when the recipe
// pins a digest, checking it as the bytes go past. Vendors who publish a
// "latest" URL cannot pin one, so an empty want means no check.
func copyVerified(dst io.Writer, src io.Reader, total int64, wantSHA256 string, progress func(string)) error {
	sum := sha256.New()
	var r io.Reader = src
	if wantSHA256 != "" {
		r = io.TeeReader(r, sum)
	}
	r = &progressReader{r: r, total: total, next: progressBytes, progress: progress}

	if _, err := io.Copy(dst, r); err != nil {
		return err
	}
	if wantSHA256 == "" {
		return nil
	}
	got := hex.EncodeToString(sum.Sum(nil))
	if !strings.EqualFold(got, wantSHA256) {
		return fmt.Errorf("sha256 mismatch\n  want %s\n  got  %s", strings.ToLower(wantSHA256), got)
	}
	return nil
}

type progressReader struct {
	r        io.Reader
	total    int64
	done     int64
	next     int64
	progress func(string)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.done >= p.next {
		p.next = p.done + progressBytes
		if p.total > 0 {
			p.progress(fmt.Sprintf("  %d/%d MB", p.done>>20, p.total>>20))
		} else {
			p.progress(fmt.Sprintf("  %d MB", p.done>>20))
		}
	}
	return n, err
}
