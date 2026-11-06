// Package mcpserver exposes voice to AI agents over MCP stdio.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

Keep each utterance short and natural, one or two sentences, unless your character or a note from register (the user works by voice and wants most of each reply aloud) says to say more; that note then also has you speak after each reply. Speak in the language register tells you your voice speaks (English unless it says otherwise, even when the conversation is not). Without a character, say what happened and what you need; details stay on screen. Never read out code, paths, URLs, logs or lists. Do not narrate routine steps, and do not repeat yourself. voice_speak returns at once while audio plays in the background: its answer says queued, not heard. To know whether the user heard it (before saying it was said, or when it matters), call voice_control status with its id.

At the start of a session call voice_control with action "register" once: it says nothing, lets the user dictate to you with their voice hotkey (their words then arrive as their own message) and tells you the name the user calls you by ("Annie, ..."); tell the user that name when they ask who you are. If the user wants to call you something else, register again with that name. When the answer gives you a character, become it for the rest of the session in every reply, written and spoken, as strongly as it describes (that also decides how you sound aloud); when the user asks for another character, call set_character. A "note" in any voice result saying your character changed applies from then on.

If the user asks for quiet, call voice_control with action "mute"; to change the voice, list presets and set one.

A story, a scene or a dialogue in several voices, or a recording to a file, goes in one voice_scene call, not in many voice_speak calls.

voice_listen asks by voice and returns what the user said. Use it only when the user wants a spoken conversation (they asked for it or answer by voice); otherwise ask in text. If it returns no speech, ask in text instead of retrying.

voice_clone makes a new voice from a recording, or designs one from a description (design). Only clone a recorded voice the user says they have the right to use; ask them, and pass rights_confirmed only after they confirm. Preview the result before switching presets; a designed voice the user does not like is removed.`

var (
	speakSchema = map[string]any{
		"type":     "object",
		"required": []string{"text"},
		"properties": map[string]any{
			"text": map[string]any{"type": "string", "description": "What to say: one or two short spoken sentences (more when register's note says the user wants more aloud), no markdown or code. May hold voice's tags: {laugh}, {sigh}, {cough}... for a sound and {pause} or {pause:1.5} for silence; a voice makes the sounds it can (voice_control list_presets says which) and drops the rest, never reading a tag aloud."},
			"mood": map[string]any{"type": "string", "enum": []string{"neutral", "happy", "excited", "amused", "teasing", "sarcastic", "calm", "tender", "sad", "angry", "afraid", "surprised"}, "description": "How text is said, where the voice's engine can show it (list_presets says which); others ignore it."},
			"intent": map[string]any{
				"type": "string", "enum": []string{core.IntentInfo, core.IntentAttention, core.IntentQuestion}, "default": core.IntentInfo,
				"description": "info: a status update; question: you are waiting for the user; attention: something needs them now. question and attention play a short chime first.",
			},
			"lang": map[string]any{
				"type":        "string",
				"description": "Language of text: en, pl, ... or auto. Omit to use the language your voice speaks (register tells you which). Languages other than English sound much worse.",
			},
			"interrupt": map[string]any{"type": "boolean", "default": false, "description": "Cut off what is playing and drop the queue. Only for urgent messages."},
			"voice":     map[string]any{"type": "string", "description": "Only when the user asks for a particular voice: a character's name (from voice_control list_characters) or a preset's name (list_presets), for this call only. Omit to speak as yourself."},
			"turn":      map[string]any{"type": "string", "description": "When you say one reply in several calls: the same id (any short word) on each piece, so they play as one utterance without a break and the transcript keeps one line. A piece joins while the one before is still queued or playing; later, or with another turn, it starts anew."},
			"display":   map[string]any{"type": "string", "description": "What the user's transcript and reading window show instead of text, when it is more than you say aloud: a role-play reply with its narration around the dialogue, say. Only text is spoken; the windows highlight the spoken words inside it."},
		},
		"additionalProperties": false,
	}
	sceneSchema = map[string]any{
		"type":     "object",
		"required": []string{"lines"},
		"properties": map[string]any{
			"lines": map[string]any{
				"type":        "array",
				"description": "The lines in order, each with its own voice (a character or a preset; omitted: yours), heard as one flowing piece. One call for the whole scene, not one per line. Text may hold voice's tags ({laugh}, {pause:1.5}) as in voice_speak. A line may end with a pause (seconds of silence after it, up to 10); a line with only a pause is a break between beats or scenes.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":   map[string]any{"type": "string"},
						"voice":  map[string]any{"type": "string"},
						"pause":  map[string]any{"type": "number", "minimum": 0, "maximum": 10, "description": "Seconds of silence after this line (default: a short breath when the voice changes)."},
						"kind":   map[string]any{"type": "string", "enum": []string{"dialogue", "narration", "thought"}, "description": "Shown, not spoken: what the line is (default dialogue)."},
						"mood":   map[string]any{"type": "string", "description": "How the line is said (neutral, happy, excited, amused, teasing, sarcastic, calm, tender, sad, angry, afraid, surprised): shown, and heard where the voice's engine can show it (list_presets says which)."},
						"action": map[string]any{"type": "string", "description": "Shown, not spoken: a stage direction (\"leans on the bar\"). A line with only an action is shown and not said."},
						"to":     map[string]any{"type": "string", "description": "Shown, not spoken: whom the line is said to."},
					},
					"additionalProperties": false,
				},
			},
			"scene": map[string]any{
				"type": "object", "description": "Optional: where and when the scene takes place, shown in the user's reading window, never spoken.",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"}, "location": map[string]any{"type": "string"}, "time": map[string]any{"type": "string"},
					"background": map[string]any{"type": "string", "description": "A short name for the place's picture (\"tavern\"); a reading-window theme may have one."},
				},
				"additionalProperties": false,
			},
			"save_to": map[string]any{"type": "string", "description": "Only when the user asks for a recording to keep or send: an absolute path ending in .wav (or .mp3, which needs ffmpeg). The scene is rendered into it instead of being heard (one line in one voice works too); the answer gives the file."},
			"mode": map[string]any{
				"type": "string", "enum": []string{"auto", "play", "render"}, "default": "auto",
				"description": "play: heard while it renders (starts soonest); render: rendered first, one engine at a time, then heard as one recording (no gaps, and GPU models are not swapped line by line; it starts later); auto: render when the scene uses two or more GPU engines (voice_control list_presets), else play.",
			},
			"lang": map[string]any{"type": "string", "description": "Language of the lines: en, pl, ... or auto. Omit for the language your voice speaks."},
		},
		"additionalProperties": false,
	}
	controlSchema = map[string]any{
		"type":     "object",
		"required": []string{"action"},
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string", "enum": []string{"register", "set_character", "list_characters", "set_style", "transcript", "status", "stop", "mute", "unmute", "list_presets", "set_preset", "list_themes", "set_theme", "send"},
				"description": "register: silently tell voice this session exists, so the user's dictation (their voice hotkey) reaches you, and get your name and character; set_character: become another character from list_characters when the user asks (the answer says who you are now); list_characters: the characters the user defined; set_style: how this session speaks, short or narrative (style); transcript: what voice said and what the user dictated, and to whom (session filters it); status: preset, mute and queue state, or with id what became of one voice_speak call (queued, rendering, playing, done, or error, stopped, interrupted, muted; the voice and engine that said it); stop: silence your own speech now, playing and queued; other sessions' speech goes on; mute/unmute: refuse or accept speech; list_presets: available voices; set_preset: switch voice for everyone using this server; list_themes: the reading window's themes; set_theme: this session's look in the user's reading window (theme; empty: the user's), e.g. a story's theme for a role-play; send: pass a message (text) to another agent session by its name (to), e.g. to ask it for a second opinion or answer its message; it reaches that agent like the user's dictation, and the transcript shows it.",
			},
			"preset":    map[string]any{"type": "string", "description": "Preset name for set_preset, from list_presets."},
			"name":      map[string]any{"type": "string", "description": "register: a name the user calls this session by when dictating (one word, e.g. one the user asked for). Omit to keep the one voice gave you."},
			"keep_warm": map[string]any{"type": "boolean", "description": "register: keep this session's voice loaded while it runs, so a GPU voice never starts cold (it holds graphics memory meanwhile). Only when the user asks for it, e.g. for a role-play spoken throughout."},
			"character": map[string]any{"type": "string", "description": "set_character: the character's name, from list_characters (name works too)."},
			"session":   map[string]any{"type": "string", "description": "transcript: only lines of this session, by its name or character (\"Rebekka\"), or \"me\" for this session; omit for every session."},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 20, "description": "transcript: how many of the newest lines."},
			"id":        map[string]any{"type": "string", "description": "status: a voice_speak answer's id, to learn whether it was heard."},
			"theme":     map[string]any{"type": "string", "description": "set_theme: a theme from list_themes, or empty for the user's."},
			"to":        map[string]any{"type": "string", "description": "send: the other session's name (as the user calls it, e.g. from a message you got)."},
			"text":      map[string]any{"type": "string", "description": "send: the message, written for an agent that does not know your work: what you ask and where to look."},
			"style":     map[string]any{"type": "string", "enum": []string{"short", "narrative"}, "description": "set_style: short (the default: a sentence or two, details on screen) or narrative (only when the user asks for a story, a scene or a role-play: speech is the content and each reply is said in full)."},
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
		Description: "Say a short sentence aloud to the user, or more when register says the user wants most of each reply aloud (for a scene in several voices, or a recording to a file: voice_scene). Returns immediately with state queued, not heard yet, and the voice and character that will say it; voice_control status with its id tells whether it was heard.",
		InputSchema: speakSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Speak", DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a service.SpeakArgs
		if err := decode(req, &a, speakSchema); err != nil {
			return fail(err), nil
		}
		a.Client, a.Preset, a.PID, a.Project = caller(req, client), presetName, callerPID(req), callerProject(req)
		return result(api.Speak(ctx, a))
	})
	s.AddTool(&mcp.Tool{
		Name:        "voice_scene",
		Description: "Only when the user asks for a story, a scene or a dialogue to hear, or a recording to keep: the lines in several voices (characters or presets) with pauses, moods and stage directions, heard as one piece in their reading window, or rendered to a file (save_to). Returns at once; voice_control status with its id shows progress and the outcome.",
		InputSchema: sceneSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Scene", DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a service.SpeakArgs
		if err := decode(req, &a, sceneSchema); err != nil {
			return fail(err), nil
		}
		if len(a.Lines) == 0 {
			return fail(errors.New("voice_scene needs lines")), nil
		}
		a.Client, a.Preset, a.PID, a.Project = caller(req, client), presetName, callerPID(req), callerProject(req)
		return result(api.Speak(ctx, a))
	})
	s.AddTool(&mcp.Tool{
		Name:        "voice_control",
		Description: "Register this session for the user's dictation and get your character, switch character, read the transcript, or check and control speech: register, set_character, list_characters, transcript, status, stop, mute, unmute, list_presets, set_preset.",
		InputSchema: controlSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Voice control", DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a service.ControlArgs
		if err := decode(req, &a, controlSchema); err != nil {
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
		if err := decode(req, &a, listenSchema); err != nil {
			return fail(err), nil
		}
		a.Client, a.PID, a.Project = caller(req, client), callerPID(req), callerProject(req)
		return result(api.Listen(ctx, a))
	})
	s.AddTool(&mcp.Tool{
		Name:        "voice_clone",
		Description: "Create and manage cloned voices: record one from the microphone, import a clip, or design one from a description. Only clone a recorded voice the user has the right to use. Afterwards, switch to it with voice_control set_preset <name>.",
		InputSchema: cloneSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Clone voice", DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a service.CloneArgs
		if err := decode(req, &a, cloneSchema); err != nil {
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

// decode accepts only the fields the tool's schema lists, read from the
// schema itself so the two cannot drift apart: internal fields of the
// service arguments (client, wait, cache, ...) are not for the model to set.
func decode(req *mcp.CallToolRequest, v any, schema map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	args := req.Params.Arguments
	if len(args) == 0 {
		args = []byte("{}")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	for k := range raw {
		if _, ok := props[k]; !ok {
			names := slices.Sorted(maps.Keys(props))
			return fmt.Errorf("invalid arguments: unknown field %q; this voice takes %s. If your tool description lists %q, voice was updated after your tools loaded: restarting this session refreshes them", k, strings.Join(names, ", "), k)
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
