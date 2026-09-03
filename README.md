<div align="center">

# terrarium

**Real, disposable computers in seconds, for you and your AI agent.**

Fork a fresh Windows or Linux machine, use it, break it, reset it to spotless in
the time it takes to read this.

[![CI](https://github.com/chryaner/terrarium/actions/workflows/ci.yml/badge.svg)](https://github.com/chryaner/terrarium/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

![one command, a real Windows machine](docs/demo/win.gif)

</div>

## Get started

You need a Windows host with [VirtualBox](https://www.virtualbox.org/) 7.x.
Do not have it yet?

```powershell
winget install Oracle.VirtualBox
```

### With Claude Code

One command. `npx` fetches the package with the terrarium binary inside, so
there is nothing else to install:

```powershell
claude mcp add -s user terrarium -- npx -y terrarium-mcp mcp
```

Open a new Claude Code session and ask for a machine:

> Fork a fresh debian-12 machine, run `uname -a` in it, then delete it.

The agent checks the host, downloads the image, builds it, forks it, and
reports back. Linux images download themselves; a Windows machine needs an
installation ISO from you, once - see [Images](#images).

### From the command line

```powershell
npm i -g terrarium-mcp
```

Then:

```console
$ terrarium doctor                 # can it talk to VirtualBox?
$ terrarium get debian-12          # download the image, boot it once, snapshot it (~1 min)
$ terrarium fork debian-12 t1      # a throwaway machine, SSH-ready in ~20s
$ terrarium exec t1 -- uname -a
$ terrarium revert t1              # back to clean in seconds
```

`ssh t1` opens a shell, `rm t1` deletes the machine, `down` and `start` park
and wake it. `fork --ttl 2h` marks an env to expire and `terrarium gc` removes
the expired ones.

No Node? [Install](#install) covers Scoop, Go, and other MCP clients.

## Why

Containers are the right way to ship a Linux service and the wrong way to test a
whole **system**: every container borrows the host's kernel, so a build that
passes in one can still fail on real hardware. VMs have always fixed that. What
they lacked was the container workflow: image, run, throw away.

terrarium is that workflow on top of the VirtualBox you already have. Fork a
golden image in a tenth of a second. Wreck the fork and put it back in seconds.
Do it from the command line, or hand the same power to an AI agent over MCP. It
ships no virtualization code, wraps `VBoxManage`, and never touches a VM it did
not create.

What people use it for:

- **Set the goal, walk away.** Give an agent a real disposable machine over MCP
  and a goal; it builds, fails, reverts, and retries on its own, unsupervised, all
  the way to installing something end to end - and the task can involve the screen,
  not just the command line.
- **Let an agent test an install end to end** on a real machine, from first boot
  to a working app.
- **Let an agent write a guide or reproduce a bug** and hand back a step-by-step
  screenshot trail or a GIF, exactly like the recordings in this README.
- **Isolated, disposable, real.** Do sensitive or risky work on a machine you can
  throw away, so it never touches your own.
- **Clean-machine testing.** Run your installer or build on a machine that has
  never seen your dev setup, and "works on my machine" stops being an argument.

## Contents

- [Get started](#get-started)
- [Features](#features)
- [Install](#install)
- [How fast](#how-fast)
- [Images](#images)
- [What it is not](#what-it-is-not)

## Features

Every recording below is real: real commands, real screenshots read from the
guest's video memory, and a timer that shows real wall-clock seconds.
[docs/demo](docs/demo/README.md) explains how they are made.

<table>
<tr>
<td width="50%" valign="top" align="center">

**Fork Windows: a desktop from cold in ~40s**

![fork a Windows machine](docs/demo/win.gif)

</td>
<td width="50%" valign="top" align="center">

**Fork Linux: SSH answering in ~20s**

![fork a Linux machine](docs/demo/linux.gif)

</td>
</tr>
<tr>
<td width="50%" valign="top" align="center">

**Brick it, then revert it: clean again in 13s**

![break a machine past boot, then revert it](docs/demo/revert.gif)

</td>
<td width="50%" valign="top" align="center">

**Claude wins Minesweeper on XP: no SSH, nothing installed**

![Claude wins Minesweeper on a Windows XP fork through the hypervisor](docs/demo/agent.gif)

</td>
</tr>
</table>

### How it compares

|                                          | **terrarium** | Docker | VirtualBox | Vagrant | Hyper-V | Multipass | WSL2 |
| ---------------------------------------- | :-----------: | :----: | :--------: | :-----: | :-----: | :-------: | :--: |
| a real, whole machine (its own kernel)   |      ✅       |   ❌   |     ✅     |   ✅    |   ✅    |    ✅     |  ❌  |
| Windows guests                           |      ✅       |   ❌   |     ✅     |   ✅    |   ✅    |    ❌     |  ❌  |
| legacy guests (XP era)                   |      ✅       |   ❌   |     ✅     |   ❌    |   ❌    |    ❌     |  ❌  |
| new machine in one command, seconds      |      ✅       |   ✅   |     ❌     |   ❌    |   ❌    |    ❌     |  ❌  |
| reset to clean state in seconds          |      ✅       |   ✅   |     ❌     |   ❌    |   ❌    |    ❌     |  ❌  |
| environment shared as a small text file  |      ✅       |   ✅   |     ❌     |   ✅    |   ❌    |    ❌     |  ❌  |
| drives GUI-only guests (screen, mouse)   |      ✅       |   ❌   |     ❌     |   ❌    |   ❌    |    ❌     |  ❌  |
| AI agent tools built in (MCP)            |      ✅       |   ❌   |     ❌     |   ❌    |   ❌    |    ❌     |  ❌  |
| production workloads                     |      ❌       |   ✅   |     ❌     |   ❌    |   ✅    |    ❌     |  ❌  |
| runs on any host OS                      |      ❌       |   ✅   |     ✅     |   ✅    |   ❌    |    ✅     |  ❌  |

If your work fits in a container, use Docker. When it needs a real, whole
machine you can break and un-break - and the last two rows are prices you can
pay - the first column is the point of this project. VirtualBox gets its own
column on purpose: terrarium runs on it, so the green half of that column is
the engine, and the red half is what this project adds.

### A dev environment per project

```yaml
# terrarium.yaml, committed in your repo
image: ubuntu-24.04
cpus: 4
memory: 4096
```

`terrarium up` forks an env named after the project, shares the project folder at
`/work` in the guest, and adds it to `~/.ssh/config`, so `ssh <project>` works
and VS Code Remote-SSH sees it one click away. `down` parks it, `up` brings it
back, `revert` resets it.

### Everyday commands

`terrarium ls` lists the machines with their guest type, and `terrarium info
<name>` reports one in full - architecture, hardware, snapshot, credentials.
`terrarium cp ./app.tar t1:/tmp/` moves files in or out over the env's own
SSH. `terrarium create s11 --iso suse.iso --ostype OpenSUSE_64` installs an
OS by hand from an ISO, see [Images](#images).

- `exec <env> --shell powershell|cmd|sh -- <command...>` runs the command
  under that shell instead of the one the guest's sshd would pick.
- `exec <env> --stdin` reads a whole script from stdin and runs it in the
  guest, so nothing in it needs escaping.
- `exec <env> --kill-on-timeout` kills the command and its children in the
  guest when the timeout fires, instead of leaving it running unwatched.
- `exec <env> --desktop` runs it in the logged-in session of a Windows guest,
  so a window or a dialog it opens is on the screen `screenshot` shows.

### Your AI agent gets real computers

Setup is one command - see [Get started](#get-started). The same binary is an
MCP server: every command above as a tool, plus
`screenshot`, `click`, `scroll`, `type`, and `keys` - screen, mouse and
keyboard injected through the hypervisor, so nothing is installed in the guest
and no network or guest additions are needed. That is how an agent drives an
installer, a login screen, or an OS too old for SSH - exactly what the
Minesweeper recording above shows. On connect the server tells the agent to run
`doctor` first and report what is missing instead of flailing.

- "Fork a clean env, build the project in it, tell me what the README missed."
- "Install our app on the Windows env and screenshot each step."

For Windows guests with SSH, `terrarium rdp` opens a full desktop already
logged in.

## Install

[Get started](#get-started) has the two fast paths. Everything else about
installing is here. terrarium is a single Windows binary; VirtualBox 7.x is
the only thing it needs on the host.

### Scoop (no Node needed)

If you do not have [Scoop](https://scoop.sh) yet, from a normal (non-admin)
PowerShell:

```powershell
Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
Invoke-RestMethod -Uri https://get.scoop.sh | Invoke-Expression
```

Then:

```powershell
scoop bucket add terrarium https://github.com/chryaner/terrarium
scoop install terrarium
```

If you installed Scoop in this same window, its shims are not on the current
shell's `PATH` yet, so `terrarium` will not be found. Open a new terminal, or
refresh `PATH` in place:

```powershell
$env:Path = [Environment]::GetEnvironmentVariable('Path','User') + ';' + [Environment]::GetEnvironmentVariable('Path','Machine')
```

The Scoop binary is the same MCP server:
`claude mcp add -s user terrarium -- terrarium mcp`.

### Go

```powershell
go install github.com/chryaner/terrarium/cmd/terrarium@latest
```

### Run it without installing

`npx` runs the CLI straight from the package cache:
`npx -y terrarium-mcp doctor`.

### Other MCP clients

Any client that launches stdio servers takes the same command. In JSON form:

```json
{
  "mcpServers": {
    "terrarium": {
      "command": "npx",
      "args": ["-y", "terrarium-mcp", "mcp"]
    }
  }
}
```

### Updating

`npm update -g terrarium-mcp` or `scoop update terrarium`. `npx -y` picks up
new versions on its own. `terrarium version` prints what you have.

### git-bash

MSYS rewrites absolute paths in arguments (`/etc/hosts` becomes
`C:/Program Files/Git/etc/hosts`) before terrarium ever sees them. Prefix the
command with `MSYS_NO_PATHCONV=1` to stop it.

## How fast

| operation                               | measured   |
| --------------------------------------- | ---------- |
| fork a golden (linked clone)            | **0.1 s**  |
| Linux fork to SSH answering (cold)      | ~20 s      |
| Windows fork to a usable desktop (cold) | ~40 s      |
| revert to clean state (RAM resume)      | **8-11 s** |
| snapshot a running machine (RAM)        | 4 s        |
| disk per fork                           | 28-48 MB   |
| build a Linux golden from scratch       | ~40 s      |

Measured on a desktop (i9-14900K, NVMe, VirtualBox 7.2). Your numbers will
differ; the shape will not. Forks are linked clones: they share the golden's
disk, which is why cloning costs a tenth of a second and megabytes, not
minutes and gigabytes.

## Images

A golden is the vendor's own published image plus a few lines of YAML, so the
bytes always come from the distribution that maintains them. Adding an image is
a pull request with one file.

### Linux: downloaded for you

`terrarium get <name>` downloads the official cloud image, seeds it with a
generated SSH key, boots it once, and snapshots it. About a minute each.

| recipe          | downloads from          |
| --------------- | ----------------------- |
| `ubuntu-24.04`  | cloud-images.ubuntu.com |
| `ubuntu-22.04`  | cloud-images.ubuntu.com |
| `debian-12`     | cloud.debian.org        |
| `debian-13`     | cloud.debian.org        |
| `alma-9`        | repo.almalinux.org      |
| `rocky-9`       | dl.rockylinux.org       |
| `fedora-44`     | fedoraproject.org       |
| `opensuse-16.0` | download.opensuse.org   |

### Windows: bring your own ISO

There is no Windows cloud image and Microsoft's media cannot be redistributed,
so you download the ISO once and terrarium runs the real installer unattended:

1. Download the installation ISO from Microsoft.
2. Save it in `%LOCALAPPDATA%\terrarium\isos\`, named for the recipe:
   `win10.iso`, `winxp.iso`.
3. `terrarium get win10`. Fully unattended: about ten minutes for win10, seven
   for XP.

A win10 golden built from this version on comes out key-based like the Linux
ones (older ones keep their password and cmd shell): the install generates
an ed25519 pair, puts the public half in the guest and records the private
one, so `ssh` and `scp` from the generated `~/.ssh/config` entry never prompt.
Its SSH sessions land in PowerShell, so `exec` quotes for PowerShell rather
than cmd.exe.

`win10` and `winxp` ship today. XP predates OpenSSH, so its
forks are driven through screenshot, click, type and keys rather than `exec`,
and its recipe needs a product key you supply in a local override. Recipe
details, private mirrors and the unattended-install internals are in
[docs/DESIGN.md](docs/DESIGN.md).

`adopt --transport guestcontrol --user <u> --password <pw>` reaches a Windows
with no SSH server through VirtualBox Guest Additions instead, if they are
installed in it, and `exec`, `cp` and the MCP tools then work on its forks.

### Install any OS from an ISO by hand

Some systems have neither a cloud image nor an installer that can be answered
in advance. `terrarium create <name> --iso <path> --ostype <type>` builds a
blank machine with the ISO in its drive and boots it, and you answer the
installer through `screenshot`, `type`, `keys` and `click` - `revert` puts the
blank disk back if you want to start over. The machine has no credentials
until the install is done: `promote` it into a golden, then `adopt` that golden
with the user and password you created inside it.

### Layer your own

A recipe can build on another image instead of on media: `from` names the
base, `setup` runs commands in a fork of it, and the result is flattened into
a new golden. The YAML file is the shareable artifact - a teammate with the
same file builds an equivalent machine from their own base, so no disk image
(and no Windows license) ever changes hands.

```yaml
# %LOCALAPPDATA%\terrarium\recipes\team-dev.yaml
name: team-dev
from: debian-12
setup:
  - sudo apt-get update
  - sudo apt-get install -y git build-essential
```

`terrarium get team-dev` builds it in seconds on top of an existing base. For
state you made by hand rather than by script, `terrarium promote <env> <name>`
flattens a configured env into a golden directly. `terrarium rm --golden
<name>` removes an image you are done with.

### Machines you already have

`terrarium adopt <vm>` records a VirtualBox VM you built yourself as a golden,
without modifying it. `terrarium import <file.ova> --name <golden>` does the
same for an appliance file: it imports and snapshots it, and seeds nothing, so
an export that predates cloud-init imports instead of hanging.

Neither needs credentials. When you do not know the login yet, adopt or import
without one, fork it, and drive the fork through the console:

```console
$ terrarium import centos6.ova --name centos6
$ terrarium fork centos6 probe
$ terrarium screenshot probe          # read the login prompt
$ terrarium type probe root --enter   # try one
$ terrarium screenshot probe          # did it take?
$ terrarium adopt trr-golden-centos6 --name centos6 --user root --password <pw>
```

Re-running `adopt` updates the record, so the last line is how a credentialless
golden becomes one `exec` and `ssh` work against. `terrarium info <name>`
reports what a golden or env actually is - guest type, architecture, hardware,
snapshot and which credentials are recorded - and `screenshot` works on any
running machine, including a golden or a VM terrarium does not manage.

## What it is not

- **Not cross-platform yet.** The host must be Windows with VirtualBox. Every
  hypervisor call sits behind one package by design, so a QEMU or Hyper-V driver
  is a planned door, not a promise.
- **Not for production.** Forks are cattle for development and testing.
- **Not a secrets manager.** A Windows golden's password is stored in plain text
  locally and is briefly visible on the `VBoxManage` command line during the
  install. Use a throwaway password.

## License

[Apache-2.0](LICENSE). Recipes, issues and field reports welcome, the most useful
contribution is a recipe for an image people actually use.
