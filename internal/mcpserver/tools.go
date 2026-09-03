package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chryaner/terrarium/internal/core"
	"github.com/chryaner/terrarium/internal/recipe"
	"github.com/chryaner/terrarium/internal/sshx"
	"github.com/chryaner/terrarium/internal/vbox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxExecOutput    = 64 << 10
	truncationMarker = "...truncated...\n"
)

type goldenInfo struct {
	Name     string `json:"name"`
	VMName   string `json:"vm_name"`
	Snapshot string `json:"snapshot"`
	SSHUser  string `json:"ssh_user,omitempty"`
	Owned    bool   `json:"owned"`
}

type envInfo struct {
	Name    string `json:"name"`
	VMName  string `json:"vm_name"`
	Golden  string `json:"golden"`
	SSHPort int    `json:"ssh_port"`
	Running bool   `json:"running"`
	Share   string `json:"share,omitempty"`
	// Expires is RFC3339, present only for an env forked with a TTL; env_gc
	// removes it once that time has passed.
	Expires string `json:"expires,omitempty"`
}

type nameInput struct {
	Name string `json:"name" jsonschema:"name of the environment"`
}

// --- doctor ---

type doctorOutput struct {
	OK     bool         `json:"ok"`
	Checks []core.Check `json:"checks"`
}

// doctorCheck deliberately does not build an engine: NewEngine fails outright
// when VBoxManage is missing, which is exactly the case this tool reports on.
func doctorCheck(ctx context.Context, _ *mcp.CallToolRequest, _ listInput) (*mcp.CallToolResult, doctorOutput, error) {
	checks := core.Doctor()
	return nil, doctorOutput{OK: core.DoctorOK(checks), Checks: checks}, nil
}

// --- recipe_list ---

type recipeInfo struct {
	Name   string `json:"name"`
	Format string `json:"format,omitempty"`
	// From marks a derived recipe: it builds on that image's golden by running
	// setup commands, rather than on downloaded media.
	From  string `json:"from,omitempty"`
	Local bool   `json:"local"`
}

type recipeListOutput struct {
	Recipes []recipeInfo `json:"recipes"`
}

func recipeList(ctx context.Context, _ *mcp.CallToolRequest, _ listInput) (*mcp.CallToolResult, recipeListOutput, error) {
	all, err := recipe.Load()
	if err != nil {
		return nil, recipeListOutput{}, err
	}
	out := recipeListOutput{Recipes: []recipeInfo{}}
	for _, r := range all {
		out.Recipes = append(out.Recipes, recipeInfo{Name: r.Name, Format: r.Format, From: r.From, Local: r.Local})
	}
	return nil, out, nil
}

// --- env_list ---

type listInput struct{}

type listOutput struct {
	Goldens []goldenInfo `json:"goldens"`
	Envs    []envInfo    `json:"envs"`
}

func envList(ctx context.Context, _ *mcp.CallToolRequest, _ listInput) (*mcp.CallToolResult, listOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, listOutput{}, err
	}
	vms, err := e.VB.ListVMs()
	if err != nil {
		return nil, listOutput{}, err
	}
	// UUIDs are authoritative, names are the fallback for records written
	// before a UUID was captured.
	running := map[string]bool{}
	for _, vm := range vms {
		if vm.Running {
			running[vm.UUID] = true
			running[vm.Name] = true
		}
	}

	out := listOutput{Goldens: []goldenInfo{}, Envs: []envInfo{}}
	for _, name := range sortedKeys(e.St.Goldens) {
		g := e.St.Goldens[name]
		out.Goldens = append(out.Goldens, goldenInfo{
			Name:     name,
			VMName:   g.VMName,
			Snapshot: g.Snapshot,
			SSHUser:  g.SSHUser,
			Owned:    g.Owned,
		})
	}
	for _, name := range sortedKeys(e.St.Envs) {
		out.Envs = append(out.Envs, describeEnv(name, e.St.Envs[name], running))
	}
	return nil, out, nil
}

func describeEnv(name string, env *core.Env, running map[string]bool) envInfo {
	info := envInfo{
		Name:    name,
		VMName:  env.VMName,
		Golden:  env.Golden,
		SSHPort: env.SSHPort,
		Running: running[env.UUID] || running[env.VMName],
		Share:   env.Share,
	}
	if !env.Expires.IsZero() {
		info.Expires = env.Expires.Format(time.RFC3339)
	}
	return info
}

// --- golden_get ---

type goldenGetInput struct {
	Image    string `json:"image" jsonschema:"image name from a recipe, for example ubuntu-24.04 or alma-9"`
	CPUs     int    `json:"cpus,omitempty" jsonschema:"CPUs for the golden image (default 2)"`
	MemoryMB int    `json:"memory_mb,omitempty" jsonschema:"memory in MB for the golden image (default 2048)"`
}

type goldenGetOutput struct {
	Golden goldenInfo `json:"golden"`
	Log    []string   `json:"log,omitempty"`
}

func goldenGet(ctx context.Context, _ *mcp.CallToolRequest, in goldenGetInput) (*mcp.CallToolResult, goldenGetOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, goldenGetOutput{}, err
	}
	// Caught here so the agent gets a usable answer: the engine's error
	// suggests --force, which only the CLI has.
	if e.St.Goldens[in.Image] != nil {
		return nil, goldenGetOutput{}, fmt.Errorf(
			"golden %q already exists and can be forked with env_fork; rebuilding it is a human decision (CLI: terrarium get %s --force)",
			in.Image, in.Image)
	}
	// Zero is passed straight through: Get picks the default for the image's
	// format, which is higher for a Windows install than for a cloud image.
	var log []string
	// force is never exposed: a rebuild throws away a golden the user may
	// have forks depending on.
	g, err := e.Get(in.Image, in.CPUs, in.MemoryMB, false, progressTo(&log))
	if err != nil {
		return nil, goldenGetOutput{}, err
	}
	return nil, goldenGetOutput{
		Golden: goldenInfo{
			Name:     in.Image,
			VMName:   g.VMName,
			Snapshot: g.Snapshot,
			SSHUser:  g.SSHUser,
			Owned:    g.Owned,
		},
		Log: log,
	}, nil
}

// --- env_fork ---

type envForkInput struct {
	Golden        string `json:"golden" jsonschema:"name of the golden image to fork, as reported by env_list"`
	Name          string `json:"name" jsonschema:"name for the new environment: letters, digits and dashes"`
	CPUs          int    `json:"cpus,omitempty" jsonschema:"CPUs, default inherits the golden image"`
	MemoryMB      int    `json:"memory_mb,omitempty" jsonschema:"memory in MB, default inherits the golden image"`
	ShareHostPath string `json:"share_host_path,omitempty" jsonschema:"absolute host directory to mount at /work inside the guest, read-write"`
	TTLSeconds    int    `json:"ttl_seconds,omitempty" jsonschema:"auto-remove the env this many seconds from now; env_gc collects it. Omit for no expiry"`
}

type envOutput struct {
	Env envInfo  `json:"env"`
	Log []string `json:"log,omitempty"`
}

func envFork(ctx context.Context, _ *mcp.CallToolRequest, in envForkInput) (*mcp.CallToolResult, envOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, envOutput{}, err
	}
	opts := core.ForkOpts{CPUs: in.CPUs, MemMB: in.MemoryMB, ShareHostPath: in.ShareHostPath}
	if in.TTLSeconds > 0 {
		opts.TTL = time.Duration(in.TTLSeconds) * time.Second
	}

	var log []string
	// Fork rolls a failure back itself, so there is nothing for env_rm to do.
	env, err := e.Fork(in.Golden, in.Name, opts, progressTo(&log))
	if err != nil {
		return nil, envOutput{}, err
	}
	if err := core.UpdateSSHConfig(e.St); err != nil {
		return nil, envOutput{}, err
	}
	return nil, envOutput{Env: describeEnv(in.Name, env, map[string]bool{env.VMName: true}), Log: log}, nil
}

// --- env_promote ---

type envPromoteInput struct {
	Name  string `json:"name" jsonschema:"env whose current state becomes the golden"`
	Image string `json:"image" jsonschema:"name for the new golden image: letters, digits, dots, dashes"`
}

type envPromoteOutput struct {
	Golden goldenInfo `json:"golden"`
	Log    []string   `json:"log,omitempty"`
}

func envPromote(ctx context.Context, _ *mcp.CallToolRequest, in envPromoteInput) (*mcp.CallToolResult, envPromoteOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, envPromoteOutput{}, err
	}
	var log []string
	g, err := e.Promote(in.Name, in.Image, progressTo(&log))
	if err != nil {
		return nil, envPromoteOutput{}, err
	}
	return nil, envPromoteOutput{
		Golden: goldenInfo{
			Name:     in.Image,
			VMName:   g.VMName,
			Snapshot: g.Snapshot,
			SSHUser:  g.SSHUser,
			Owned:    g.Owned,
		},
		Log: log,
	}, nil
}

// --- env_gc ---

type gcInput struct {
	DryRun bool `json:"dry_run,omitempty" jsonschema:"list what would be removed without removing it"`
}

type gcOutput struct {
	Removed []core.GCRemoval `json:"removed"`
	DryRun  bool             `json:"dry_run"`
}

func envGC(ctx context.Context, _ *mcp.CallToolRequest, in gcInput) (*mcp.CallToolResult, gcOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, gcOutput{}, err
	}
	removed, err := e.GC(in.DryRun, func(string) {})
	if err != nil {
		return nil, gcOutput{}, err
	}
	if !in.DryRun {
		if err := core.UpdateSSHConfig(e.St); err != nil {
			return nil, gcOutput{}, err
		}
	}
	if removed == nil {
		removed = []core.GCRemoval{}
	}
	return nil, gcOutput{Removed: removed, DryRun: in.DryRun}, nil
}

// --- env_start ---

func envStart(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, envOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, envOutput{}, err
	}
	var log []string
	env, err := e.Start(in.Name, progressTo(&log))
	if err != nil {
		return nil, envOutput{}, err
	}
	return nil, envOutput{Env: describeEnv(in.Name, env, map[string]bool{env.VMName: true}), Log: log}, nil
}

// --- env_exec ---

type envExecInput struct {
	Name       string `json:"name" jsonschema:"name of the environment to run in"`
	Command    string `json:"command,omitempty" jsonschema:"shell command to run in the guest; omit when passing script"`
	Script     string `json:"script,omitempty" jsonschema:"multi-line script to run instead of command; it reaches the shell on stdin so nothing in it is quoted or split"`
	Shell      string `json:"shell,omitempty" jsonschema:"run under this shell instead of the guest's own: powershell, cmd or sh"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"seconds to wait before giving up (default 300)"`
}

type envExecOutput struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

func envExec(ctx context.Context, _ *mcp.CallToolRequest, in envExecInput) (*mcp.CallToolResult, envExecOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, envExecOutput{}, err
	}
	port, user, password, key, err := e.SSHTarget(in.Name)
	if err != nil {
		return nil, envExecOutput{}, err
	}
	command, stdin, err := execRequest(in, func() (string, error) { return e.ShellFor(in.Name) })
	if err != nil {
		return nil, envExecOutput{}, err
	}

	timeout := core.DefaultExecTimeout
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}

	var out sshx.OutputBuffer
	code, err := sshx.ExecScript(ctx, timeout, port, user, password, key, command, stdin, &out, &out)
	if err != nil {
		var timedOut *sshx.TimeoutError
		if errors.As(err, &timedOut) {
			// Whatever it managed to print is the only clue about where it
			// got stuck, so it goes in the error.
			return nil, envExecOutput{}, fmt.Errorf("%w; output so far:\n%s", err, tail(out.String()))
		}
		return nil, envExecOutput{}, err
	}
	return nil, envExecOutput{ExitCode: code, Output: tail(out.String())}, nil
}

// execRequest turns the tool's input into the command an SSH session carries
// and, for a script, what to feed its stdin. guestShell reports what the
// guest's own sshd hands a command line to, and is only called when the
// answer is needed.
//
// A plain command with no shell named is passed through untouched: the guest's
// own shell reads it, which is what the description promises and what the
// pipes and redirects in it depend on. A script, and anything under a named
// shell that can read one, goes over stdin instead - the one route where no
// shell but the intended one ever parses the text, and the reason these
// parameters exist.
func execRequest(in envExecInput, guestShell func() (string, error)) (string, io.Reader, error) {
	if (in.Command == "") == (in.Script == "") {
		return "", nil, fmt.Errorf("pass exactly one of command or script")
	}
	if in.Shell == "" && in.Script == "" {
		return in.Command, nil, nil
	}
	if in.Shell == core.ShellCmd {
		// cmd.exe reads no script from stdin. A command line it can run, but
		// only through whatever shell sshd hands that line to first. Asked for
		// implicitly - the guest simply runs cmd - a script gets PowerShell
		// instead, which every such guest also has.
		if in.Script != "" {
			return "", nil, fmt.Errorf("shell cmd cannot run a script: use powershell, or a plain command")
		}
		return "cmd /c " + in.Command, nil, nil
	}
	shell, err := requestedShell(in.Shell, guestShell)
	if err != nil {
		return "", nil, err
	}
	text := in.Script
	if text == "" {
		text = in.Command
	}
	return core.ScriptCommand(shell), strings.NewReader(text), nil
}

// requestedShell resolves the shell parameter, asking the guest only when it
// was left out. sh is what the parameter is documented as; posix is what a
// golden records.
func requestedShell(want string, guestShell func() (string, error)) (string, error) {
	switch want {
	case "":
		return guestShell()
	case "sh":
		return core.ShellPOSIX, nil
	}
	if !core.ValidShell(want) {
		return "", fmt.Errorf("unknown shell %q: one of powershell, cmd, sh", want)
	}
	return want, nil
}

// tail keeps the end of the output: that is where the error is.
func tail(s string) string {
	if len(s) <= maxExecOutput {
		return s
	}
	return truncationMarker + s[len(s)-maxExecOutput:]
}

// --- env_down / env_revert / env_rm ---

type actionOutput struct {
	Name string   `json:"name"`
	Log  []string `json:"log,omitempty"`
}

// --- console: env_screenshot / env_type / env_keys ---

type screenshotOutput struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func envScreenshot(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, screenshotOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, screenshotOutput{}, err
	}
	// VBoxManage writes the file itself, so it needs a real path; the image
	// only has to live long enough to be read back.
	dir, err := os.MkdirTemp("", "terrarium-shot")
	if err != nil {
		return nil, screenshotOutput{}, err
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "screen.png")
	if err := e.Screenshot(in.Name, path); err != nil {
		return nil, screenshotOutput{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, screenshotOutput{}, err
	}

	var out screenshotOutput
	if cfg, err := png.DecodeConfig(bytes.NewReader(data)); err == nil {
		out.Width, out.Height = cfg.Width, cfg.Height
	}
	// Data carries the raw PNG: the SDK base64-encodes []byte on the wire.
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: "image/png"}},
	}, out, nil
}

type typeInput struct {
	Name       string `json:"name" jsonschema:"name of the environment"`
	Text       string `json:"text" jsonschema:"text to type, as if at the keyboard"`
	PressEnter bool   `json:"press_enter,omitempty" jsonschema:"press enter after the text"`
}

func envType(ctx context.Context, _ *mcp.CallToolRequest, in typeInput) (*mcp.CallToolResult, actionOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	if err := e.TypeText(in.Name, in.Text, in.PressEnter); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name}, nil
}

type keysInput struct {
	Name string   `json:"name" jsonschema:"name of the environment"`
	Keys []string `json:"keys" jsonschema:"key names to press in order; each entry is one keystroke or chord"`
}

func envKeys(ctx context.Context, _ *mcp.CallToolRequest, in keysInput) (*mcp.CallToolResult, actionOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	if err := e.PressKeys(in.Name, in.Keys); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name}, nil
}

type clickInput struct {
	Name   string `json:"name" jsonschema:"name of the environment"`
	X      int    `json:"x" jsonschema:"pixel column, 0-based, as seen in env_screenshot"`
	Y      int    `json:"y" jsonschema:"pixel row, 0-based, as seen in env_screenshot"`
	Button string `json:"button,omitempty" jsonschema:"left (default), right or middle"`
	Double bool   `json:"double,omitempty" jsonschema:"double-click"`
}

func envClick(ctx context.Context, _ *mcp.CallToolRequest, in clickInput) (*mcp.CallToolResult, actionOutput, error) {
	button := vbox.MouseLeft
	switch in.Button {
	case "", "left":
	case "right":
		button = vbox.MouseRight
	case "middle":
		button = vbox.MouseMiddle
	default:
		return nil, actionOutput{}, fmt.Errorf("button must be left, right or middle, got %q", in.Button)
	}
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	if err := e.Click(in.Name, in.X, in.Y, button, in.Double); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name}, nil
}

type scrollInput struct {
	Name   string `json:"name" jsonschema:"name of the environment"`
	X      int    `json:"x" jsonschema:"pixel column to scroll at, 0-based"`
	Y      int    `json:"y" jsonschema:"pixel row to scroll at, 0-based"`
	Clicks int    `json:"clicks" jsonschema:"wheel clicks: positive scrolls down, negative up"`
}

func envScroll(ctx context.Context, _ *mcp.CallToolRequest, in scrollInput) (*mcp.CallToolResult, actionOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	if err := e.MoveMouse(in.Name, in.X, in.Y); err != nil {
		return nil, actionOutput{}, err
	}
	if err := e.Scroll(in.Name, in.Clicks); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name}, nil
}

// --- env_snapshot / env_restore ---

type snapInput struct {
	Name string `json:"name" jsonschema:"name of the environment"`
	Snap string `json:"snap" jsonschema:"name of the snapshot"`
}

func envSnapshot(ctx context.Context, _ *mcp.CallToolRequest, in snapInput) (*mcp.CallToolResult, actionOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	if err := e.Snapshot(in.Name, in.Snap); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name}, nil
}

func envRestore(ctx context.Context, _ *mcp.CallToolRequest, in snapInput) (*mcp.CallToolResult, actionOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	var log []string
	if err := e.RestoreSnap(in.Name, in.Snap, progressTo(&log)); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name, Log: log}, nil
}

func envDown(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, actionOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	if err := e.Down(in.Name); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name}, nil
}

func envRevert(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, actionOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	var log []string
	if err := e.Revert(in.Name, progressTo(&log)); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name, Log: log}, nil
}

func envRm(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, actionOutput, error) {
	e, err := engine()
	if err != nil {
		return nil, actionOutput{}, err
	}
	if err := e.Remove(in.Name); err != nil {
		return nil, actionOutput{}, err
	}
	if err := core.UpdateSSHConfig(e.St); err != nil {
		return nil, actionOutput{}, err
	}
	return nil, actionOutput{Name: in.Name}, nil
}
