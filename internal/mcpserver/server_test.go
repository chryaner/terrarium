package mcpserver

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect wires a client to the real server over the SDK's in-memory
// transport. Only the tool list is exercised: every handler reaches
// VBoxManage, which tests must not.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTr, clientTr := mcp.NewInMemoryTransports()

	ss, err := newServer().Connect(ctx, serverTr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientTr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func listTools(t *testing.T) map[string]*mcp.Tool {
	t.Helper()
	res, err := connect(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func TestToolsAdvertised(t *testing.T) {
	tools := listTools(t)

	want := []string{
		"doctor", "env_click", "env_create", "env_down", "env_exec", "env_fork",
		"env_gc", "env_keys", "env_list", "env_promote", "env_pull", "env_push",
		"env_restore", "env_revert", "env_rm", "env_screenshot", "env_scroll",
		"env_snapshot", "env_start", "env_type", "golden_adopt", "golden_get",
		"golden_import", "recipe_list",
	}
	for _, name := range want {
		if tools[name] == nil {
			t.Errorf("tool %q is not advertised", name)
		}
	}
	if len(tools) != len(want) {
		t.Errorf("expected %d tools, got %d", len(want), len(tools))
	}
}

// Instructions arrive in the initialize result, so they are the one thing an
// agent reads before deciding what to call first.
func TestServerInstructions(t *testing.T) {
	got := connect(t).InitializeResult().Instructions
	if got == "" {
		t.Fatal("no instructions sent to the client")
	}
	for _, want := range []string{"doctor", "ok:false", "rm --golden"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions should mention %q:\n%s", want, got)
		}
	}
}

// The reported misuse: an agent believed adopt needed working credentials up
// front, so a machine whose login nobody knew looked impossible to take on.
// The instructions are the only place it reads before choosing a first call.
func TestInstructionsCoverUnknownCredentials(t *testing.T) {
	got := connect(t).InitializeResult().Instructions
	for _, want := range []string{"golden_import", "golden_adopt", "env_screenshot", "env_type"} {
		if !strings.Contains(got, want) {
			t.Errorf("the unknown-credentials workflow should name %q:\n%s", want, got)
		}
	}
}

// Over the wire the schema arrives as plain JSON, which is what the agent
// actually reads.
func schemaOf(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("tool %q: input schema is %T, want a JSON object", tool.Name, tool.InputSchema)
	}
	return schema
}

func requiredOf(schema map[string]any) []string {
	raw, _ := schema["required"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func propertiesOf(schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	return props
}

// Agents pick tools by reading descriptions, and act on the side effects a
// description promises. An undescribed tool is an unusable one.
func TestToolsDescribed(t *testing.T) {
	for name, tool := range listTools(t) {
		if len(tool.Description) < 40 {
			t.Errorf("tool %q has a thin description: %q", name, tool.Description)
		}
		if got := schemaOf(t, tool)["type"]; got != "object" {
			t.Errorf("tool %q input schema type is %v, want object", name, got)
		}
	}
}

func TestToolSchemas(t *testing.T) {
	tools := listTools(t)

	cases := []struct {
		tool     string
		required []string
		optional []string
	}{
		{"env_list", nil, nil},
		{"recipe_list", nil, nil},
		{"doctor", nil, nil},
		{"golden_get", []string{"image"}, []string{"cpus", "memory_mb"}},
		{"env_create", []string{"name", "iso_path", "ostype"}, []string{"disk_gb", "cpus", "memory_mb"}},
		{"env_push", []string{"name", "local_path", "guest_path"}, []string{"recursive"}},
		{"env_pull", []string{"name", "guest_path", "local_path"}, []string{"recursive"}},
		{"golden_import", []string{"ova_path", "name"}, []string{"user", "password", "key"}},
		// Only the VM is required: adopting without credentials is the first
		// half of working out an unknown login, not a mistake.
		{"golden_adopt", []string{"vm"}, []string{"name", "snapshot", "user", "password", "key", "take_snapshot"}},
		{"env_fork", []string{"golden", "name"}, []string{"cpus", "memory_mb", "share_host_path", "ttl_seconds"}},
		{"env_start", []string{"name"}, nil},
		// command is optional because script is the alternative to it;
		// execRequest is what enforces exactly one of the two.
		{"env_exec", []string{"name"}, []string{"command", "script", "shell", "timeout_sec"}},
		{"env_down", []string{"name"}, nil},
		{"env_revert", []string{"name"}, nil},
		{"env_rm", []string{"name"}, nil},
		{"env_promote", []string{"name", "image"}, nil},
		{"env_gc", nil, []string{"dry_run"}},
		{"env_screenshot", []string{"name"}, nil},
		{"env_type", []string{"name", "text"}, []string{"press_enter"}},
		{"env_keys", []string{"name", "keys"}, nil},
		{"env_click", []string{"name", "x", "y"}, []string{"button", "double"}},
		{"env_scroll", []string{"name", "x", "y", "clicks"}, nil},
		{"env_snapshot", []string{"name", "snap"}, nil},
		{"env_restore", []string{"name", "snap"}, nil},
	}
	for _, c := range cases {
		tool := tools[c.tool]
		if tool == nil {
			t.Errorf("tool %q missing", c.tool)
			continue
		}
		schema := schemaOf(t, tool)
		required := requiredOf(schema)
		props := propertiesOf(schema)

		for _, field := range c.required {
			if !slices.Contains(required, field) {
				t.Errorf("%s: %q should be required, required=%v", c.tool, field, required)
			}
			if _, ok := props[field]; !ok {
				t.Errorf("%s: %q missing from properties", c.tool, field)
			}
		}
		for _, field := range c.optional {
			if slices.Contains(required, field) {
				t.Errorf("%s: %q should be optional, required=%v", c.tool, field, required)
			}
			if _, ok := props[field]; !ok {
				t.Errorf("%s: %q missing from properties", c.tool, field)
			}
		}
		if len(required) != len(c.required) {
			t.Errorf("%s: required=%v, want exactly %v", c.tool, required, c.required)
		}
	}
}

func TestTailKeepsTheEnd(t *testing.T) {
	short := "all good\n"
	if got := tail(short); got != short {
		t.Errorf("short output must pass through, got %q", got)
	}

	long := make([]byte, maxExecOutput+500)
	for i := range long {
		long[i] = 'a'
	}
	copy(long[len(long)-6:], []byte("BOOM!\n"))

	got := tail(string(long))
	if len(got) != maxExecOutput+len(truncationMarker) {
		t.Errorf("truncated length %d, want %d", len(got), maxExecOutput+len(truncationMarker))
	}
	if got[:len(truncationMarker)] != truncationMarker {
		t.Errorf("missing truncation marker: %q", got[:len(truncationMarker)])
	}
	if got[len(got)-6:] != "BOOM!\n" {
		t.Errorf("tail lost the end of the output: %q", got[len(got)-6:])
	}
}
