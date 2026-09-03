// Package mcpserver puts an MCP face on the same engine the CLI drives, so an
// agent can create and drive environments without shelling out. Like the CLI
// it holds no VM logic of its own.
package mcpserver

import (
	"context"
	"sort"
	"strings"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/chryaner/terrarium/internal/keys"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Serve runs the server on stdio until the client disconnects. Nothing may be
// written to stdout: the transport owns it.
func Serve() error {
	return newServer().Run(context.Background(), &mcp.StdioTransport{})
}

// instructions reach the client in the initialize result, before any tool is
// called. An agent flailing against a broken VirtualBox install wastes a lot
// of everyone's time, and the fix is almost always something only the user can
// do.
const instructions = `terrarium runs disposable VirtualBox VMs on this machine.

Call the doctor tool once at the start of a session, before any other tool. If
it reports ok:false, stop: tell the user which check failed and quote its fix
verbatim. Do not try to work around a failing check, and do not call other
tools until it passes - they will fail in less obvious ways.

Environments are disposable and cheap to recreate. A guest with SSH
credentials takes env_exec; one without is driven through env_screenshot,
env_type, env_keys, env_click and env_scroll.

An OS with no golden image can be installed by hand: env_create makes a blank
machine with an ISO in its drive, the console tools drive the installer, and
env_promote turns the finished machine into a golden. That golden starts with
no credentials, so tell the user to record them with
` + "`terrarium adopt <vm> --user <user> [--password <pw> | --key <path>]`" + ` in the
CLI before env_exec will work on its forks.

Golden images are the opposite: durable, disk-heavy, and owned by the user.
Nothing here removes one - that is the user's call, via terrarium rm --golden
in the CLI - so create one (golden_get, golden_import, env_promote) only when
the task asks for it, name it something the user will recognise, and report
what was created.

Credentials are not needed up front. For a machine whose login nobody knows -
an appliance someone exported years ago, an old VM - call golden_import or
golden_adopt with no user or password, env_fork it, read the login prompt with
env_screenshot, and try a guess with env_type. Once something works, call
golden_adopt again with the user and password that worked: re-running it
updates the record, and every env forked after that can use env_exec. Never
guess at credentials in a golden record - record only what you saw work.`

func newServer() *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "terrarium", Version: core.Version},
		&mcp.ServerOptions{Instructions: instructions},
	)

	mcp.AddTool(s, &mcp.Tool{
		Name: "doctor",
		Description: "Check that this machine can run terrarium at all: VirtualBox present and responding, " +
			"state directory writable, SSH client available. Call this once before anything else. " +
			"Cheap and safe to call at any time. If it reports ok:false, report the failing check and " +
			"its fix to the user rather than working around it.",
	}, doctorCheck)

	mcp.AddTool(s, &mcp.Tool{
		Name: "recipe_list",
		Description: "List the images that CAN be built into golden images with golden_get. This is the " +
			"catalogue of what is available to build; env_list reports what has actually been built and " +
			"what can be forked right now. Read-only.",
	}, recipeList)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_list",
		Description: "List the golden images available to fork from and the environments that exist, " +
			"with their SSH ports and whether each is currently running. Read-only.",
	}, envList)

	mcp.AddTool(s, &mcp.Tool{
		Name: "golden_get",
		Description: "Build a golden image from a recipe so environments can be forked from it. " +
			"Slow and bandwidth-heavy: downloads roughly 500 MB the first time, then imports the appliance, " +
			"boots it once for cloud-init, shuts it down and snapshots it. Takes minutes, and a Windows " +
			"image takes closer to an hour because it runs a real installer. A recipe with `from` instead " +
			"builds on an existing golden: it forks the base, runs the recipe's setup commands in the fork, " +
			"and flattens the result into the new golden. Use recipe_list for the names " +
			"that work here. Fails if the golden already exists - rebuilding one is a decision for the human.",
	}, goldenGet)

	mcp.AddTool(s, &mcp.Tool{
		Name: "golden_import",
		Description: "Register an .ova or .ovf appliance file already on this machine as a golden image. " +
			"Unlike golden_get there is no recipe and no download, and nothing is seeded: an appliance " +
			"exported from somewhere else has no cloud-init to wait for. The VM is imported, snapshotted " +
			"where it stands and recorded as a golden env_fork can use. " +
			"Credentials are optional - import without them, then work the login out through env_fork, " +
			"env_screenshot and env_type, and record it with golden_adopt. " +
			"Creates something durable that only the user can remove, so do it when the task asks for it.",
	}, goldenImport)

	mcp.AddTool(s, &mcp.Tool{
		Name: "golden_adopt",
		Description: "Record a VirtualBox VM that already exists on this machine as a golden image env_fork " +
			"can use. The VM itself is not modified unless take_snapshot is set. " +
			"Re-running it updates the record, which is how credentials are added later: adopt a machine " +
			"with no user or password, fork it, read its login prompt with env_screenshot, try a guess " +
			"with env_type, then adopt again with what worked. " +
			"Records nothing about the guest that was not observed - never invent a user or password here.",
	}, goldenAdopt)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_fork",
		Description: "Create a new environment from a golden image and boot it. Takes under a minute and " +
			"costs tens of MB of disk. The environment is disposable: anything done inside it is lost on " +
			"env_rm, and env_revert rewinds it to the state it had right after this call. " +
			"If the golden has no SSH credentials recorded - env_list shows an empty ssh_user, which is " +
			"normal for an old or GUI-only system - env_exec will not work on the result. Drive it with " +
			"env_screenshot, env_type, env_keys and env_click instead.",
	}, envFork)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_create",
		Description: "Create a blank environment with an installation ISO in its DVD drive and boot it, for " +
			"an OS that has no recipe and no unattended installer - an old distribution, or one whose " +
			"installer has to be answered by hand. The boot order is disk first, DVD second, so the " +
			"installer runs while the disk is blank and the installed system boots itself afterwards. " +
			"The result has no golden image and therefore no credentials: env_exec will not work on it. " +
			"Drive the installer with env_screenshot, env_type, env_keys and env_click, and note that " +
			"env_revert puts the blank disk back and restarts the install. When the OS is up, env_promote " +
			"turns it into a golden - which the user then gives credentials with `terrarium adopt`. " +
			"iso_path is a path on this host, where the server runs.",
	}, envCreate)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_push",
		Description: "Copy a file or directory from this host into an environment, over SFTP on the " +
			"environment's own SSH connection. local_path is on the host, where this server runs; " +
			"guest_path is inside the guest and always uses forward slashes, Windows guests included " +
			"(C:/Users/terrarium/setup.exe). Missing parent directories in the guest are created. " +
			"Set recursive for a directory. Needs the same credentials env_exec needs.",
	}, envPush)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_pull",
		Description: "Copy a file or directory out of an environment onto this host, over SFTP on the " +
			"environment's own SSH connection. guest_path is inside the guest and always uses forward " +
			"slashes, Windows guests included; local_path is on the host, where this server runs, and " +
			"its missing parent directories are created. Set recursive for a directory. " +
			"Needs the same credentials env_exec needs.",
	}, envPull)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_start",
		Description: "Boot a stopped environment and wait until SSH answers. Does nothing if it is already " +
			"running. Fails if no environment by that name exists - use env_fork to create one.",
	}, envStart)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_exec",
		Description: "Run a shell command inside an environment over SSH and return its exit code and output. " +
			"The command is one shell string: pipes, redirects, globs and quoting are interpreted by the " +
			"guest shell, unlike the CLI's `terrarium exec`, which passes its arguments literally. " +
			"Which shell that is: /bin/sh on Linux guests; on a Windows guest, the shell recorded for its " +
			"golden, which is PowerShell for a golden terrarium built and cmd.exe for an older or adopted " +
			"one, unless `terrarium adopt --shell` said otherwise. Pass shell (powershell, cmd or sh) to " +
			"run under a different one, and script instead of command for anything multi-line or heavily " +
			"quoted - a script reaches the shell on stdin, so nothing in it is re-parsed on the way. " +
			"The environment must be running. Commands run as a user with passwordless sudo, so this can " +
			"change or destroy anything inside the guest - the host is not affected. " +
			"A command that outruns timeout_sec is killed in the guest, with its child processes, and the " +
			"error says what was killed: nothing is left running where you cannot see it. " +
			"On a Windows guest an ordinary command runs in session 0, which has no screen: if it opens a " +
			"window or a dialog it waits there forever and env_screenshot shows nothing. Set desktop to run " +
			"it in the session a user is logged into instead, where env_screenshot can see what it wants. " +
			"Only works when the environment's golden has SSH credentials; without them, use " +
			"env_screenshot, env_type, env_keys and env_click.",
	}, envExec)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_screenshot",
		Description: "Capture a running machine's screen and return it as an image. This is how to see a guest " +
			"that has no SSH - it needs nothing of the guest, not even a network. " +
			"The name can be an environment, a golden image, or any VirtualBox VM by name: reading a screen " +
			"changes nothing, so this one is safe to point at a machine terrarium does not manage. The " +
			"input tools (env_type, env_keys, env_click, env_scroll) are environments only. " +
			"The machine must be running. The screen lags behind keystrokes, so after env_type or env_keys " +
			"take a fresh screenshot to see what actually happened.",
	}, envScreenshot)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_type",
		Description: "Type text into the environment's keyboard, as if a person were at it - the keystrokes go " +
			"wherever the guest's focus happens to be, so check with env_screenshot first. Environments " +
			"only: to type into a golden image, env_fork it and type into the fork. The environment " +
			"must be running. Takes effect a moment after the call returns.",
	}, envType)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_keys",
		Description: "Press keys and chords in the environment that env_type cannot express. Each entry in the " +
			"list is one keystroke, and chords land as chords. The environment must be running. " +
			"Valid key names: " + strings.Join(keys.Summary(), ", ") + " (the ctrl run is literal: ctrl-x, " +
			"ctrl-d and so on). " +
			"For the mouse, use env_click and env_scroll.",
	}, envKeys)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_click",
		Description: "Click the environment's screen at a pixel position, 0-based, exactly as seen in " +
			"env_screenshot. Left button unless button says otherwise; double for a double-click. Like " +
			"env_screenshot it goes through the hypervisor and needs nothing of the guest, but the click " +
			"lands wherever the pixel says, so screenshot first and aim. The environment must be running.",
	}, envClick)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_scroll",
		Description: "Scroll the environment's mouse wheel: the pointer moves to the given pixel position, then " +
			"the wheel turns there. Positive clicks scroll down, negative up. The environment must be " +
			"running.",
	}, envScroll)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_snapshot",
		Description: "Take a named snapshot of an environment, to come back to later with env_restore. A " +
			"running environment has its RAM captured too, so restoring resumes rather than reboots. " +
			"Cheap: seconds, and only the changed disk blocks.",
	}, envSnapshot)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_restore",
		Description: "Rewind an environment to a named snapshot taken with env_snapshot. Everything done since " +
			"that snapshot is lost. Use env_revert instead to go back to the environment's original " +
			"clean state.",
	}, envRestore)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_down",
		Description: "Shut an environment down cleanly, keeping its disk and its place in the environment list. " +
			"env_start brings it back with everything intact.",
	}, envDown)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_revert",
		Description: "Discard everything done inside an environment since it was forked, returning it to its " +
			"clean snapshot. Files written in the guest are lost. Takes seconds, because the snapshot " +
			"includes RAM and the machine resumes rather than reboots.",
	}, envRevert)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_rm",
		Description: "Destroy an environment and delete its disks. Everything inside it is gone for good. " +
			"The golden image it was forked from is untouched.",
	}, envRm)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_promote",
		Description: "Flatten an environment's current state into a new golden image that env_fork can use, " +
			"so a machine configured during this session becomes a reusable fork source. A full disk " +
			"copy: takes minutes and the disk of a golden, and afterwards depends on nothing. The " +
			"environment is shut down first and left in place; remove it with env_rm if it is no longer " +
			"needed once promoted. Promote only when the user wants the machine kept: the result is " +
			"durable and only the user can remove it.",
	}, envPromote)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_gc",
		Description: "Remove environments that have outlived the TTL set at fork time (ttl_seconds), plus any " +
			"whose VM has vanished from VirtualBox. Environments forked without a TTL are never removed by " +
			"age. Pass dry_run to see what would go without removing it. Good to call before finishing a " +
			"session so short-lived envs do not pile up.",
	}, envGC)

	return s
}

// Each handler builds its own engine: state on disk is shared with the CLI,
// which the user may be running against the same environments concurrently.
func engine() (*core.Engine, error) {
	return core.NewEngine()
}

// progressTo collects engine progress lines for the tool result. Agents get
// the same narration a human sees in the terminal.
func progressTo(log *[]string) func(string) {
	return func(msg string) { *log = append(*log, msg) }
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
