package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"voice/internal/core"
	"voice/internal/preset"
	"voice/internal/service"
)

// newTestSession connects a client in memory; audio is recorded to a temp
// file so the test does not use the speakers.
func newTestSession(t *testing.T) (*mcp.ClientSession, *core.Speaker) {
	t.Helper()
	cfg, err := preset.Load("")
	if err != nil {
		t.Fatal(err)
	}
	sp := core.NewSpeaker(cfg)
	sp.RecordTo(filepath.Join(t.TempDir(), "out.wav"))
	t.Cleanup(sp.Close)

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := New(&service.Service{Speaker: sp, Config: cfg}, "test", "", "").Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, sp
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.StructuredContent != nil {
		t.Errorf("%s: structuredContent must stay empty", name)
	}
	if len(res.Content) != 1 {
		t.Fatalf("%s: %d content blocks", name, len(res.Content))
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func TestTools(t *testing.T) {
	cs, _ := newTestSession(t)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 5 { // speak, scene, control, listen, clone
		t.Fatalf("tools: %d", len(tools.Tools))
	}
	// voice_speak is for short messages: a scene goes to voice_scene.
	if out, isErr := call(t, cs, "voice_speak", map[string]any{"text": "Hi.", "lines": []any{map[string]any{"text": "x"}}}); !isErr {
		t.Errorf("voice_speak took lines: %s", out)
	}
	if out, isErr := call(t, cs, "voice_scene", map[string]any{"lines": []any{}}); !isErr {
		t.Errorf("voice_scene took no lines: %s", out)
	}

	out, isErr := call(t, cs, "voice_control", map[string]any{"action": "list_presets"})
	if isErr || !strings.Contains(out, `"calm-mature"`) {
		t.Errorf("list_presets: %s", out)
	}
	if out, isErr = call(t, cs, "voice_control", map[string]any{"action": "set_preset", "preset": "system"}); isErr {
		t.Fatalf("set_preset: %s", out)
	}
	out, isErr = call(t, cs, "voice_speak", map[string]any{"text": "Done.", "intent": "question"})
	var ticket struct {
		ID string `json:"id"`
	}
	if isErr || json.Unmarshal([]byte(out), &ticket) != nil || ticket.ID == "" {
		t.Errorf("speak: %s", out)
	}

	call(t, cs, "voice_control", map[string]any{"action": "mute"})
	if out, isErr = call(t, cs, "voice_speak", map[string]any{"text": "Hello."}); !isErr || !strings.Contains(out, "muted") {
		t.Errorf("speak while muted: %s", out)
	}
	if out, isErr = call(t, cs, "voice_control", map[string]any{"action": "set_preset", "preset": "nope"}); !isErr {
		t.Errorf("unknown preset accepted: %s", out)
	}
}

func TestSchemaRejectsBadIntent(t *testing.T) {
	cs, _ := newTestSession(t)
	if out, isErr := call(t, cs, "voice_speak", map[string]any{"text": "x", "intent": "loud"}); !isErr {
		t.Errorf("bad intent accepted: %s", out)
	}
}

// Every argument the tools decode must be declared in their schema, or a
// client that follows the schema cannot send it.
func TestSchemasDeclareDecodedFields(t *testing.T) {
	props := controlSchema["properties"].(map[string]any)
	for _, f := range []string{"action", "preset", "name", "character"} {
		if _, ok := props[f]; !ok {
			t.Errorf("voice_control schema lacks %q", f)
		}
	}
}

// Every field a tool's schema offers is accepted, and nothing else: send's
// to and text once fell through a hand-kept list.
func TestDecodeFollowsSchema(t *testing.T) {
	req := func(args string) *mcp.CallToolRequest {
		return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(args)}}
	}
	var a service.ControlArgs
	if err := decode(req(`{"action":"send","to":"Anna","text":"hi"}`), &a, controlSchema); err != nil || a.To != "Anna" || a.Text != "hi" {
		t.Fatalf("send's fields: %v %+v", err, a)
	}
	if err := decode(req(`{"action":"status","client":"x"}`), &a, controlSchema); err == nil {
		t.Fatal("an internal field was accepted")
	}
}
