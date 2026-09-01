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

Golden images are the opposite: durable, disk-heavy, and owned by the user.
Nothing here removes one - that is the user's call, via terrarium rm --golden
in the CLI - so create one (golden_get, env_promote) only when the task asks
for it, name it something the user will recognise, and report what was
created.`

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
		Name: "env_fork",
		Description: "Create a new environment from a golden image and boot it. Takes under a minute and " +
			"costs tens of MB of disk. The environment is disposable: anything done inside it is lost on " +
			"env_rm, and env_revert rewinds it to the state it had right after this call. " +
			"If the golden has no SSH credentials recorded - env_list shows an empty ssh_user, which is " +
			"normal for an old or GUI-only system - env_exec will not work on the result. Drive it with " +
			"env_screenshot, env_type, env_keys and env_click instead.",
	}, envFork)

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
			"The environment must be running. Commands run as a user with passwordless sudo, so this can " +
			"change or destroy anything inside the guest - the host is not affected. " +
			"Only works when the environment's golden has SSH credentials; without them, use " +
			"env_screenshot, env_type, env_keys and env_click.",
	}, envExec)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_screenshot",
		Description: "Capture the environment's screen and return it as an image. This is how to see a guest " +
			"that has no SSH - it needs nothing of the guest, not even a network. The environment must be " +
			"running. The screen lags behind keystrokes, so after env_type or env_keys take a fresh " +
			"screenshot to see what actually happened.",
	}, envScreenshot)

	mcp.AddTool(s, &mcp.Tool{
		Name: "env_type",
		Description: "Type text into the environment's keyboard, as if a person were at it - the keystrokes go " +
			"wherever the guest's focus happens to be, so check with env_screenshot first. The environment " +
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
