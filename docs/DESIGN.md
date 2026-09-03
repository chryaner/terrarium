# terrarium - design

## Where it sits

Virtualization is a layer cake: hardware virtualization (CPU) → hypervisor
(VirtualBox, KVM, Hyper-V) → management interface (VBoxManage, libvirt) →
user experience. terrarium is the top layer only - like Vagrant, Multipass,
and Quickemu before it. It contains no virtualization code: it makes an
existing hypervisor pleasant.

VBoxManage is not replaced; terrarium runs it as a child process:

```
you / VS Code / AI agent
        │
    terrarium  (single Go binary - the only thing you touch)
        │  runs VBoxManage as a child process,
        │  parses, retries, times out, enforces policy
        ▼
 VBoxManage → VBoxSVC → VirtualBox → the machines
```

## Architecture

```
cmd/terrarium/    entrypoint
recipes/          image recipes as data: one YAML file per image, embedded
                  into the binary. Adding an image is a PR with no Go in it.
internal/cli/     cobra commands (thin - no VM logic)
internal/vbox/    THE driver: every VBoxManage interaction lives here and
                  nowhere else. Parsing, timeouts, quirks - quarantined.
internal/core/    engine: goldens, forks, snapshots, console, readiness,
                  state, project files, ssh-config, RDP launch, doctor
internal/sshx/    SSH exec, keys; readiness = SSH banner
internal/seed/    cloud-init seed ISO generation, natively in Go
internal/mcpserver/ MCP server on stdio, over the same core
internal/recipe/  recipe loading: embedded set + the user's own
internal/keys/    key names to scancodes, for guests without SSH
tools/vmgif/      renders the README demos from real VM screenshots
```

## Images are recipes, not payloads

terrarium never redistributes an operating system. A golden is the vendor's
own published cloud image plus a recipe - name, URL, format, and a few
optional fields - so the bytes always come from the distribution that
maintains them, and a stale mirror is impossible by construction.

Recipes are data. The built-in ones are embedded from `recipes/`, and a user's
own are read from `%LOCALAPPDATA%\terrarium\recipes\`, where a file named
after a built-in overrides it. That makes the two things people actually want
cheap: contributing an image upstream is a one-file PR, and pointing an image
at a private mirror needs no fork.

Most distributions publish qcow2 rather than an appliance. VirtualBox cannot
boot qcow2, so `get` converts it with `clonemedium` (measured: ~3 s for
AlmaLinux 9, since cloud images are mostly empty) and builds a VM around the
resulting VDI on an AHCI controller.

## Windows goldens run the real installer

There is no cloud image for Windows, so the third format is `iso` and the
build is an unattended installation: `VBoxManage unattended install` generates
an answer file from its own templates, boots the installer headless and drives
it through to a working desktop. terrarium then reaches the guest the way it
reaches every other one - over SSH - by having the post-install step enable
Windows' built-in OpenSSH server.

Three things about that mechanism are worth writing down, because none of them
are obvious and all of them cost time to rediscover:

1. **The post-install command runs at first logon, as the created user.**
   VirtualBox's `win_nt6_unattended.xml` puts it in `FirstLogonCommands`, and
   the account it creates is in `administrators;users` with autologon
   configured - so it runs with an elevated token, which is what
   `Add-WindowsCapability` needs. It is *not* SYSTEM, and it is not the
   `specialize` pass.
2. **The command is pasted verbatim into a batch file.** Anything cmd.exe
   treats as a metacharacter (`%`, `&`, `|`, `<`, `>`, `^`) is a hazard, so
   the provisioning command is a single quoted `powershell -Command` that
   contains none of them. A custom `--post-install-template` would allow
   richer scripting, but replacing Oracle's template means either losing the
   guest-additions install it performs or copying a GPL-3 file into this
   Apache-2.0 repository. Neither is worth it for one command.
3. **VirtualBox does not eject the install media.** Its own template carries
   `rem rem @todo eject DVD install media` where the eject should be, so the
   installation ISO and the answer-file floppy are still attached when setup
   finishes. terrarium ejects them itself before snapshotting - a golden that
   boots the installer again on every fork is not a golden.

The post-install does three more things with that one command line, and they
are what makes a Windows golden behave like a Linux one. It writes the
generated ed25519 public key into
`%ProgramData%\ssh\administrators_authorized_keys` - the only
`AuthorizedKeysFile` sshd's stock config reads for an account in the
administrators group - and resets that file's ACL to Administrators and SYSTEM,
without which sshd ignores it and says nothing. It points
`HKLM\SOFTWARE\OpenSSH\DefaultShell` at `powershell.exe`, so a command sent
over SSH is parsed once by PowerShell instead of three times by cmd.exe. The
golden keeps the password as well as the key, because RDP and the console
still authenticate with it.

That is also why goldens carry a `shell` field. Quoting an argv is
shell-specific and there is no spelling that works for more than one of them,
so the record says which: `posix`, `cmd` or `powershell`. Goldens terrarium
built know their own answer; an adopted or older one is asked once, with
`echo %COMSPEC%` - cmd expands it, PowerShell echoes it back - and the answer
is written to state so no later command pays for it.

The install took ten and a half minutes measured on a fast desktop, and can
plausibly take half an hour on modest hardware, against roughly 40 seconds for
a Linux cloud image. That is the price of a real installer, and it is paid
once per image: forks of the finished snapshot boot at the usual speed.

Windows ISOs cannot be redistributed, so an `iso` recipe carries a relative
`path` instead of a URL, resolved against `%LOCALAPPDATA%\terrarium\isos\`.
The shipped `win10` recipe means "whatever ISO the user saved as win10.iso
there", and the error when it is missing says exactly where to put it.

Windows before OpenSSH existed (XP, 2000) cannot be reached the usual way, so
an `ssh: false` recipe builds a **credless** golden. The post-install command
is just a shutdown, so the machine powering itself off is the completion
signal the way a Linux clean shutdown is; the golden is recorded with no
credentials and its forks are driven through the console tools rather than
`exec`. XP also predates AHCI, so it installs from an IDE disk (a SATA boot
disk bluescreens setup with 0x7B), skips the guest additions current
VirtualBox no longer ships for it, and needs a product key, which the shipped
recipe leaves to a local override since terrarium can neither ship one nor
install without it.

Windows goldens are deliberately **not sysprepped**. Every fork shares the
golden's machine SID, computer name and SSH host key. For terrarium's job -
isolated, NAT-only, disposable machines - that is harmless. It is wrong the
moment forks join a domain or share a network segment; anyone needing that
should run `sysprep /generalize` inside the golden before forking, and accept
the slower first boot per fork that comes with it.

## Goldens from goldens

A configured machine is worth keeping and worth sharing: the env with the
team's toolchain installed, the machine where the bug reproduces. Configured
state comes in two kinds - state you can write down as commands, and state
you arrived at by hand - so there are two ways to capture it.

A **derived recipe** has `from` and `setup` instead of media: building it
forks the base golden, runs the setup commands in the fork over SSH, flattens
the result into a new golden, and removes the scratch fork (left in place for
inspection when a command fails). The YAML file is the unit of sharing: a
teammate with the same recipe builds an equivalent golden from their own
base, so configured state travels as reviewable text and no disk image
changes hands. That is also what makes it the legal way to share a configured
Windows machine - each person builds from their own ISO under their own
license. A credless base (XP) cannot run `setup`, since there is nothing to
exec through; those forks are configured by console and captured with
`promote`.

**`promote <env> <image>`** flattens an env's current state into a golden by
hand instead of by script - for the state you arrived at interactively and
could not have written down. `rm --golden` completes the lifecycle, refused
while forks depend on the image.

Both paths flatten with a full clone rather than keeping the result as
another linked layer. The linked version would be nearly free, but it would
make one golden depend on another: delete the base and the derived image dies
with it, and every fork reads through the extra disk layer. A golden's
defining property is that it depends on nothing, so the copy is paid once at
build time - seconds to minutes - and `rm` stays trivially safe everywhere.
Forks of the promoted golden are the usual 0.1 s linked clones.

## The console layer, and what it cannot do

A guest with no SSH is still a guest terrarium should be able to drive: the
author's XP images, an OS halfway through its installer, an application being
tested through its GUI. `VBoxManage` can capture the screen and inject
keystrokes with no cooperation from the guest - no additions, no network, no
agent - so that is the fallback path, and it is what makes forks of a
credentialless adopted VM useful rather than merely creatable.

Two constraints shape it:

- **The mouse costs one COM call.** `controlvm` has `keyboardputstring`,
  `keyboardputscancode` and `screenshotpng`, but nothing for the mouse:
  pointer control exists only on the `IMouse` COM interface. VBoxManage
  itself is just another COM client, so `click` and `scroll` go to the same
  place directly: a short-lived, thread-locked session in
  `internal/vbox/mouse_windows.go`, opened and released per call like a
  VBoxManage invocation, so there is no connection to go stale when VBoxSVC
  idles out. It is deliberately the only COM in the codebase, and it stays
  behind the same driver seam. Absolute pointing needs a USB tablet device,
  which most images do not ship, so `fork` adds one to every clone before
  the clean snapshot.
- **No VRDE.** VirtualBox's own remote display would give a proper remote
  console, but the VRDP server ships in Oracle's proprietary extension pack,
  under a licence that is free only for personal use. Depending on it would
  quietly make terrarium unusable at work. `terrarium rdp` instead turns on the
  *guest's* own RDP server over SSH and forwards a port to it, which needs
  nothing beyond the OSE build - and, being SSH-driven, works only where SSH
  already does.

Getting `terrarium rdp` to open a session with no prompts took two attempts.
The obvious approach - write an .rdp file carrying the credentials and hand it
to mstsc - stopped working in April 2026, when a security update
(CVE-2026-26151, an RDP-file phishing mitigation) made mstsc show a security
dialog on *every* .rdp file it opens and removed the "don't ask again" tick box
for unsigned files. That is the mitigation working as designed: the only
supported way to suppress it is to sign the file. The dialog appears before any
saved credential is tried, so a perfectly good password blob never gets looked
at.

Microsoft's own FAQ names the way out - the update "only affects connections
started by opening an RDP file", not ones started from the client. So terrarium
launches `mstsc /v:127.0.0.1:<port>` with no file at all, and puts what the
session needs where mstsc already looks:

- **The password** goes into Windows Credential Manager under
  `TERMSRV/127.0.0.1`, the entry mstsc's own "Remember me" writes. The
  Credential Manager API is not wrapped by `x/sys/windows`, so those calls are
  declared against `advapi32.dll` directly.
- **The certificate** is pre-trusted by speaking just enough RDP to get the
  guest into TLS - a 19-byte X.224 connection request asking for TLS or
  CredSSP - reading the certificate it presents, and writing its SHA-256
  thumbprint to `HKCU\...\Terminal Server Client\Servers\<host>\CertHash`.
  That is where mstsc records a certificate when the user ticks "don't ask me
  again": observed behaviour rather than documented API, and the first thing to
  suspect if the warning ever comes back. It has already moved once - a decade
  of written-down lore says CertHash is a SHA-1 thumbprint, and the 2026 client
  writes SHA-256 and ignores 20-byte values (learned by seeding SHA-1, getting
  the warning anyway, and diffing what mstsc wrote after the tick box).

Both steps are best effort. A credential that will not save costs a login
prompt, an untrusted certificate costs a one-time warning, and neither stops
the connection, so they are reported as a note rather than an error. Forks
share their golden's certificate - nothing is sysprepped - so one `CertHash`
seed holds for every env forked from the same golden.

The .rdp file is still written for `--no-launch` and other clients, carrying
the password as a DPAPI `password 51:b:` blob - what "Remember me" produces,
decryptable only by this user on this machine, and better than the plaintext
already in `state.json`. It is simply no longer what gets launched. On
non-Windows hosts DPAPI does not exist and that line is omitted.

Scancodes are generated from the set 1 table in `internal/keys` rather than
hand-written: a key's break code is its make code with bit 7 set, extended keys
carry an `e0` prefix on both halves, and a chord is modifiers-down, key-down,
key-up, modifiers-up-in-reverse. Deriving it all from that rule means a stuck
modifier is a bug in four lines of code rather than in eighty hex pairs.

Forking a credentialless golden skips the readiness wait entirely - there is no
banner coming - and settles for 15 seconds before taking the clean snapshot, so
the revert target is a booting machine rather than a BIOS screen.

`screenshot` accepts any running VM - an env, a golden, or one terrarium does
not manage - because reading the framebuffer changes nothing, and the first
question about a machine with an unknown login is "what is on its screen".
The input verbs stay bound to envs: typing into a golden would mutate the one
thing the model promises never changes, and a fork costs a tenth of a second.

All VBoxManage specifics stay behind `internal/vbox`. The rest of the code
speaks abstract verbs (`Fork`, `Exec`, `WaitReady`), so a future QEMU or
Hyper-V driver slots in without touching anything above it.

## Reaching a guest: transports, files and the exec runtime

Everything above assumes SSH, and SSH is still the default: it needs nothing
installed by terrarium and works the same for every guest that has an sshd.
Three field reports from the first weeks of use were about guests that do
not, or commands that SSH cannot control once started, and this is what they
changed.

**Transports.** A golden carries a `transport`: blank or `ssh`, or
`guestcontrol`, which runs commands through VirtualBox Guest Additions
(`VBoxManage guestcontrol run`) and copies files with `copyto`/`copyfrom`.
It exists for Windows guests that have Guest Additions and no sshd - XP or
Windows 7 machines the user built by hand - and it is set on `adopt`, never
guessed. The readiness wait for such a fork asks for the additions' version
property instead of an SSH banner. Its cost is that the guest password rides
on the VBoxManage command line for every call, which is why it is opt-in and
why the console tools remain the answer for anything without additions. The
recorded `shell` still decides quoting; the transport only decides who
launches the shell.

**Files.** `cp` uses SFTP on the same connection and credentials as `exec`,
so anything exec can reach cp can reach, and a Windows guest works because
OpenSSH's stock config already ships an SFTP subsystem. The spec syntax is
scp's `env:path`; a single character before the colon is a drive letter, not
an env, which costs one-letter env names their spec and nothing else.

**Kill on timeout.** An SSH session cannot cancel what it started: closing it
leaves the process running, and a Windows command that opened a dialog runs
forever. So a command that may be killed is tagged with a marker the guest
can see - a trailing comment in PowerShell and sh, `& rem trr:<id>` in cmd -
and when the deadline passes a second session finds every process whose
command line carries the marker and kills the tree (`taskkill /T`, or `pkill
-f`). The marker is the only handle that works across all three shells
without an agent in the guest. The MCP server always kills: an agent cannot go
and look at a hung process, so a leaked one is strictly worse than a killed
one.

**Desktop mode.** SSH sessions on Windows run in session 0, where a window is
invisible and a dialog blocks forever.
`exec --desktop` runs the command as a scheduled task marked interactive, so
it lands in the logged-in session that `screenshot` shows, with its output
redirected to a file the normal session reads back. It needs someone logged
on at the console; new goldens get Winlogon autologon in the post-install for
exactly that, and older ones get an error that says how to log in through
the console tools.

**Hand installs and appliances.** `create --iso` builds a blank machine with
the ISO in its drive and boots it, recorded as an env with no golden and so
no credentials: an installation in progress, driven through the console
tools. Boot order is disk first, DVD second, so the empty disk falls through
to the installer and the finished install boots itself with no eject step;
`revert` lands on the blank disk, which is "start the install over". Once
installed it is `promote`d into a golden and `adopt`ed with the account
created inside it. `import` takes an OVA or OVF the same way `adopt` takes a
VM: it imports, snapshots and records, and seeds nothing, because a legacy
export has no cloud-init and waiting for one is exactly how importing a
vendor appliance hangs. Both are terrarium's own VMs (owned, so `rm` deletes
them), unlike an adopted one.

**What a record says.** Goldens and envs cache the VirtualBox guest type, and
`info` derives the architecture from it, because the one thing nobody could
see before the first Windows install failed was that the recipe pinned a
32-bit guest under an x64 ISO. The build now asks the ISO (`unattended
detect`) and the detected type wins any disagreement about architecture.

## Safety policy

Rules enforced by the driver. Each comes from an incident during the
2026-08-19 benchmark:

1. **Never `--live` snapshots.** A live snapshot hung at 90% and wedged
   VBoxSVC so hard that read-only commands blocked; recovery required killing
   processes. A normal snapshot pauses the VM ~4 s and is reliable.
2. **Every call has a hard timeout.** When VBoxSVC wedges, even `showvminfo`
   hangs forever. Callers get an error, never a hang.
3. **Only touch what we made.** terrarium-managed VMs are namespaced and
   tracked in a state file. A user's hand-built VM is never modified, only
   `adopt`-ed (which merely records it as a golden source).
4. **Started ≠ ready.** `startvm` returns in ~6 s; SSH answers ~50 s later
   (cold) or ~8 s (RAM resume). Readiness means reading an `SSH-` banner -
   VirtualBox NAT accepts TCP connects before the guest listens, so a plain
   port probe lies.
5. **Retry the lock, don't fight it.** Concurrent VBoxManage calls against one
   VM fail with a "locked" error. The driver retries the handful of operations
   that race in practice (`RestoreCurrent`, `SnapshotRestore`, `Unregister`) a
   few times with a short backoff. The state file has its own protection:
   every save takes an exclusive lock file, re-reads the current file and
   reapplies only this process's changes onto it, so a CLI command and a
   long-running MCP server interleave freely. The lock is held for the
   milliseconds of a write, never for the length of an operation.

## Field notes (benchmark, 2026-08-19)

Host: Windows 11, i9-14900K, 64 GB, NVMe. VirtualBox 7.2.0. Golden image:
CentOS 7, 22.7 GB.

- full clone: 51 s; linked clone (fork): 0.1 s, 28-48 MB disk after boot
- cold boot → SSH: 46-57 s; 3 parallel forks all ready in 50-59 s
- online snapshot (RAM, no --live): 4 s, ~220 MB `.sav`
- restore + resume → SSH: 8.3 s ("instant-on")
- offline snapshot / revert / destroy: ≈ 0.1-0.3 s each
- snapshot **delete** is the hidden heavy op: it merges disk layers -
  instant when diffs are small, minutes after long divergence, and blocked
  entirely while linked clones depend on that snapshot (must track)
- Ubuntu cloud OVA: 27 s download (566 MB), 8 s import, ~10 s to login
  prompt - but unreachable without a cloud-init seed (no user, no SSH host
  keys). Seed generation is therefore a core feature, not a nicety.
- `clonevm --basefolder` places forks on another disk in the same 0.1 s,
  but the base image stays where it is: goldens belong on the big disk.

Additional notes from bringing up the cloud-image path:

- Attaching a DVD to the Ubuntu OVA's PIIX4 IDE controller hangs the noble
  kernel dead at the ATAPI probe (~t=3.6s, forever). The same ISO on an AHCI
  SATA controller probes cleanly. The seed therefore always goes on a
  dedicated `trrseed` AHCI controller.
- The OVA ships a phantom floppy controller; it costs ~2s of boot and a
  stream of fd0 I/O errors. Removed at build time.
- The qcow2 conversion boots fine, verified on Debian, AlmaLinux, Rocky,
  Fedora and openSUSE goldens.
- Debian's cloud images come in two kernels: the `genericcloud` build is
  stripped for hypervisors that paravirtualize everything and hangs in the
  initramfs on VirtualBox, while the `generic` build carries the drivers to
  boot. The recipes point at `generic`.
- `get` timings, from scratch (download included): Debian ~40 s, Fedora ~42 s,
  openSUSE ~53 s, Rocky ~150 s. Forks boot to SSH in ~20 s, revert in ~10 s.

## Why not …

- **Fork/patch VirtualBox?** Ten million lines of C++, Windows kernel-driver
  signing, an eternal merge treadmill against a vendor that develops behind
  closed doors - and every reliability fix we need lives comfortably one
  layer up. Upstream gets bug reports, not forks.
- **The VirtualBox COM API instead of the CLI?** Worse documentation, hostile
  from Go (manual reference counting, thread affinity, session locking), and
  it deepens the coupling the driver seam exists to contain. Vagrant drove
  VBoxManage in production for a decade; the CLI is enough - except for the
  mouse, which the CLI simply does not have, so that one capability talks COM
  and nothing else does.
- **QEMU/Hyper-V first?** The author's daily machines are VirtualBox on
  Windows (guests from XP to modern Linux). Build for the real workflow
  first; the driver seam keeps the door open.
