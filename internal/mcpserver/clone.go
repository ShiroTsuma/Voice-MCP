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
			"type": "string", "enum": []string{"record", "add", "list", "preview", "set_transcript", "make_prompt", "remove"},
			"description": "record: record the user from the microphone (three ticks count down, a chime starts and ends it), check the quality and add it as a cloned voice; tell the user to read a few natural sentences aloud first. add: import an existing clip as a cloned voice (creates preset clone-<name>); list: stored voices; preview: say a sample with a voice; set_transcript: store what the clip says; make_prompt: make the voice's saved prompt again (qwen3 keeps one, so the voice speaks without its recording; add makes it already); remove: delete a voice.",
		},
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
		"text": map[string]any{"type": "string", "description": "preview: sentence to say (optional)."},
	},
	"additionalProperties": false,
}
