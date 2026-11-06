package mcpserver

import (
	"strings"

	"voice/sidecar"
)

// cloneEngineHelp describes the cloning engines, the GPU ones from
// sidecar/backends.json.
func cloneEngineHelp() string {
	var gpu []string
	for _, b := range sidecar.Backends() {
		if !b.Clones || b.Hidden {
			continue
		}
		s := b.Name + " (" + b.Note
		if b.Transcript {
			s += "; needs the transcript"
		}
		gpu = append(gpu, s+")")
	}
	return "add/record: cloning engine. pocket (default, CPU, English) needs no transcript; zipvoice (CPU) needs one. On the GPU sidecar, when set up: " + strings.Join(gpu, "; ") + "."
}

var cloneSchema = map[string]any{
	"type":     "object",
	"required": []string{"action"},
	"properties": map[string]any{
		"action": map[string]any{
			"type": "string", "enum": []string{"record", "add", "design", "list", "preview", "set_transcript", "make_prompt", "describe", "rename", "remove", "groups", "group", "rename_group", "remove_group"},
			"description": "record: record the user from the microphone (three ticks count down, a chime starts and ends it), check the quality and add it as a cloned voice; tell the user to read a few natural sentences aloud first. add: import an existing clip as a cloned voice (creates a preset of the same name); design: make a new voice from a description in words (no recording, no rights question): it plays a preview, then ask the user whether to keep it and remove it if not; list: stored voices, each with its group; preview: say a sample with a voice; set_transcript: store what the clip says; make_prompt: make the voice's saved prompt again (qwen3 keeps one, so the voice speaks without its recording; add makes it already); describe: store the user's description of the voice (empty clears it), shown with it and in list_presets; rename: give the voice new_name (characters and settings that used it follow); remove: delete a voice. The user files voices in their own groups (\"D&D\", \"ASMR\"): groups: the groups in order, each with its voices; group: put the voice name in group (empty group: in none), or without name make an empty group; rename_group: group to new_group (into an existing one: merged); remove_group: drop group, its voices stay, in no group.",
		},
		"group":     map[string]any{"type": "string", "description": "group: the group to put the voice in; add, record, design: the group the new voice goes in (made when new); rename_group, remove_group: the group. Use the user's existing group names (groups lists them)."},
		"new_group": map[string]any{"type": "string", "description": "rename_group: the group's new name."},
		"seconds":    map[string]any{"type": "number", "minimum": 6, "maximum": 30, "description": "record: length of the recording in seconds (default 12)."},
		"name":       map[string]any{"type": "string", "description": "Voice name: lowercase letters, digits, - or _ (max 40)."},
		"clip_path":  map[string]any{"type": "string", "description": "add: absolute path to the recording (WAV; other formats need ffmpeg), or to a reference .json with \"audio\" and \"text\". 3-15 s of one clear speaker works best."},
		"transcript": map[string]any{"type": "string", "description": "add/set_transcript: exact words spoken in the clip. Optional for pocket, required for zipvoice and the GPU engines marked so below."},
		"engine": map[string]any{
			"type":        "string",
			"description": cloneEngineHelp(),
		},
		"rights_confirmed": map[string]any{
			"type":        "boolean",
			"description": "add: true only if the user explicitly told you they have the right to use this person's voice (their own voice, or the speaker's consent). Never assume it; ask the user.",
		},
		"text":        map[string]any{"type": "string", "description": "preview: sentence to say; design: what the sample reads, about ten seconds (both optional)."},
		"description": map[string]any{"type": "string", "description": "design: the voice in words: age, gender, timbre, pace, mood, accent (\"a raspy older woman, slow and dry, faint British accent\"). English works best. describe, add, record: the user's description of the voice."},
		"new_name":    map[string]any{"type": "string", "description": "rename: the voice's new name (lowercase letters, digits, - or _)."},
		"lang":        map[string]any{"type": "string", "description": "design: the sample's language (default en; Polish is not among Qwen3-TTS's)."},
	},
	"additionalProperties": false,
}
