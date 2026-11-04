// Package mcpserver exposes voice to AI agents over MCP stdio.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"voice/internal/core"
	"voice/internal/service"
)

// Instructions tell agents when speaking helps and when it is noise.
const Instructions = `voice speaks through the user's speakers. It is the agent's mouth: use it so the user notices you without watching the screen.

Speak when:
- you finish a long or multi-step task (intent "info"),
- you need the user's decision, approval or input (intent "question"),
- something needs attention now, such as a failure that blocks you (intent "attention").

Keep each utterance to one or two short, natural sentences, in English (the voices are English even when the conversation is not). Say what happened and what you need; details stay on screen. Never read out code, paths, URLs, logs or lists. Do not narrate routine steps, and do not repeat yourself. voice_speak returns at once while audio plays in the background.

At the start of a session call voice_control with action "register" once: it says nothing, lets the user dictate to you with their voice hotkey (their words then arrive as their own message) and tells you the name the user calls you by ("Annie, ..."); tell the user that name when they ask who you are. If the user wants to call you something else, register again with that name. When the answer gives you a character, become it for the rest of the session in every reply, written and spoken, as it describes; when the user asks for another character, call set_character. A "note" in any voice result saying your character changed applies from then on.

If the user asks for quiet, call voice_control with action "mute"; to change the voice, list presets and set one.

voice_listen asks by voice and returns what the user said. Use it only when the user wants a spoken conversation (they asked for it or answer by voice); otherwise ask in text. If it returns no speech, ask in text instead of retrying.

voice_clone makes a new voice from a recording. Only clone a voice the user says they have the right to use; ask them, and pass rights_confirmed only after they confirm. Preview the result before switching presets.`

var (
	speakSchema = map[string]any{
		"type":     "object",
		"required": []string{"text"},
		"properties": map[string]any{
			"text": map[string]any{"type": "string", "description": "What to say: one or two short spoken sentences, no markdown or code."},
			"intent": map[string]any{
				"type": "string", "enum": []string{core.IntentInfo, core.IntentAttention, core.IntentQuestion}, "default": core.IntentInfo,
				"description": "info: a status update; question: you are waiting for the user; attention: something needs them now. question and attention play a short chime first.",
			},
			"lang": map[string]any{
				"type": "string", "enum": []string{"en", "pl", "auto"},
				"description": "Language of text. Omit to use the configured default (English). Other languages sound much worse.",
			},
			"interrupt": map[string]any{"type": "boolean", "default": false, "description": "Cut off what is playing and drop the queue. Only for urgent messages."},
		},
		"additionalProperties": false,
	}
	controlSchema = map[string]any{
		"type":     "object",
		"required": []string{"action"},
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string", "enum": []string{"register", "set_character", "list_characters", "status", "stop", "mute", "unmute", "list_presets", "set_preset"},
				"description": "register: silently tell voice this session exists, so the user's dictation (their voice hotkey) reaches you, and get your name and character; set_character: become another character from list_characters when the user asks (the answer says who you are now); list_characters: the characters the user defined; status: preset, mute and queue state; stop: silence now and clear the queue; mute/unmute: refuse or accept speech; list_presets: available voices; set_preset: switch voice for everyone using this server.",
			},
			"preset":    map[string]any{"type": "string", "description": "Preset name for set_preset, from list_presets."},
			"name":      map[string]any{"type": "string", "description": "register: a name the user calls this session by when dictating (one word, e.g. one the user asked for). Omit to keep the one voice gave you."},
			"character": map[string]any{"type": "string", "description": "set_character: the character's name, from list_characters (name works too)."},
		},
		"additionalProperties": false,
	}
)

func boolPtr(b bool) *bool { return &b }

// clientMetaKey is where the shared-mcp gateway puts the calling client's name
// (clientInfo.name, e.g. "claude-code" or "codex-mcp-client") in each tools/call,
// and clientPIDMetaKey the agent's process ID.
const (
	clientMetaKey    = "io.github.shirotsuma/client"
	clientPIDMetaKey = "io.github.shirotsuma/client_pid"
	clientCwdMetaKey = "io.github.shirotsuma/client_cwd"
)

// callerProject is the agent's working directory name (its project): what the
// gateway passes, or, without a gateway, this server's own directory.
func callerProject(req *mcp.CallToolRequest) string {
	if req.Params != nil {
		if cwd, ok := req.Params.GetMeta()[clientCwdMetaKey].(string); ok && cwd != "" {
			return filepath.Base(cwd)
		}
		if _, viaGateway := req.Params.GetMeta()[clientMetaKey]; viaGateway {
			return ""
		}
	}
	cwd, _ := os.Getwd()
	return filepath.Base(cwd)
}

// callerPID is the agent process behind this call: the one the gateway names,
// or, without a gateway, this server's parent (the agent started it).
func callerPID(req *mcp.CallToolRequest) uint32 {
	if req.Params != nil {
		meta := req.Params.GetMeta()
		if pid, ok := meta[clientPIDMetaKey].(float64); ok && pid > 0 {
			return uint32(pid)
		}
		if _, viaGateway := meta[clientMetaKey]; viaGateway {
			return 0 // an older gateway: its parent is not the agent
		}
	}
	return uint32(os.Getppid())
}

// caller names the client of this call for [clients]: the server's -client
// when set, else the name the gateway passed along.
func caller(req *mcp.CallToolRequest, fixed string) string {
	if fixed != "" || req.Params == nil {
		return fixed
	}
	name, _ := req.Params.GetMeta()[clientMetaKey].(string)
	return name
}

// New builds the server. api runs the operations (normally the daemon
// client); client names the MCP client so [clients] can give it its own
// voice, and presetName, when set, is the voice of this server's speech.
func New(api service.API, version, client, presetName string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "voice", Version: version}, &mcp.ServerOptions{Instructions: Instructions})
	s.AddTool(&mcp.Tool{
		Name:        "voice_speak",
		Description: "Say a short sentence aloud to the user. Returns immediately; playback is queued.",
		InputSchema: speakSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Speak", DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a service.SpeakArgs
		if err := decode(req, &a, "text", "intent", "lang", "interrupt"); err != nil {
			return fail(err), nil
		}
		a.Client, a.Preset, a.PID, a.Project = caller(req, client), presetName, callerPID(req), callerProject(req)
		return result(api.Speak(ctx, a))
	})
	s.AddTool(&mcp.Tool{
		Name:        "voice_control",
		Description: "Register this session for the user's dictation and get your character, switch character, or check and control speech: register, set_character, list_characters, status, stop, mute, unmute, list_presets, set_preset.",
		InputSchema: controlSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Voice control", DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a service.ControlArgs
		if err := decode(req, &a, "action", "preset", "name", "character"); err != nil {
			return fail(err), nil
		}
		a.Client, a.PID, a.Project = caller(req, client), callerPID(req), callerProject(req)
		return result(api.Control(ctx, a))
	})
	s.AddTool(&mcp.Tool{
		Name:        "voice_listen",
		Description: "Ask the user by voice and get their spoken answer as text. Opens the microphone only for this call, with a chime at start and end. Use only when the user wants to talk by voice.",
		InputSchema: listenSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Listen", DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a service.ListenArgs
		if err := decode(req, &a, "prompt", "wait_seconds", "max_seconds"); err != nil {
			return fail(err), nil
		}
		a.Client, a.PID, a.Project = caller(req, client), callerPID(req), callerProject(req)
		return result(api.Listen(ctx, a))
	})
	s.AddTool(&mcp.Tool{
		Name:        "voice_clone",
		Description: "Create and manage cloned voices: record one from the microphone or import a clip. Only clone a voice the user has the right to use. After record or add, switch to it with voice_control set_preset clone-<name>.",
		InputSchema: cloneSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Clone voice", DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a service.CloneArgs
		if err := decode(req, &a, "action", "name", "clip_path", "transcript", "engine", "rights_confirmed", "text", "seconds"); err != nil {
			return fail(err), nil
		}
		return result(api.Clone(ctx, a))
	})
	return s
}

func result(v any, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return fail(err), nil
	}
	if s, ok := v.(string); ok {
		return text(s), nil
	}
	return text(compact(v)), nil
}

// decode accepts only the schema's fields: internal fields of the service
// arguments (client, wait, cache, ...) are not for the model to set.
func decode(req *mcp.CallToolRequest, v any, allowed ...string) error {
	args := req.Params.Arguments
	if len(args) == 0 {
		args = []byte("{}")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	for k := range raw {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("invalid arguments: unknown field %q", k)
		}
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// Results go to the model once, as content; no structuredContent copy.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func fail(err error) *mcp.CallToolResult {
	r := text(err.Error())
	r.IsError = true
	return r
}

func compact(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
