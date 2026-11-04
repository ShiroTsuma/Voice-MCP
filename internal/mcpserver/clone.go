package mcpserver

var cloneSchema = map[string]any{
	"type":     "object",
	"required": []string{"action"},
	"properties": map[string]any{
		"action": map[string]any{
			"type": "string", "enum": []string{"record", "add", "list", "preview", "set_transcript", "remove"},
			"description": "record: record the user from the microphone (three ticks count down, a chime starts and ends it), check the quality and add it as a cloned voice; tell the user to read a few natural sentences aloud first. add: import an existing clip as a cloned voice (creates preset clone-<name>); list: stored voices; preview: say a sample with a voice; set_transcript: store what the clip says; remove: delete a voice.",
		},
		"seconds":    map[string]any{"type": "number", "minimum": 6, "maximum": 30, "description": "record: length of the recording in seconds (default 12)."},
		"name":       map[string]any{"type": "string", "description": "Voice name: lowercase letters, digits, - or _ (max 40)."},
		"clip_path":  map[string]any{"type": "string", "description": "add: absolute path to the recording (WAV; other formats need ffmpeg). 3-15 s of one clear speaker works best."},
		"transcript": map[string]any{"type": "string", "description": "add/set_transcript: exact words spoken in the clip. Optional for pocket, required for zipvoice."},
		"engine": map[string]any{
			"type":        "string",
			"description": "add: cloning engine. pocket (default, CPU) needs no transcript; zipvoice and f5 need one; chatterbox and xtts (GPU sidecar, if set up) also speak Polish.",
		},
		"rights_confirmed": map[string]any{
			"type":        "boolean",
			"description": "add: true only if the user explicitly told you they have the right to use this person's voice (their own voice, or the speaker's consent). Never assume it; ask the user.",
		},
		"text": map[string]any{"type": "string", "description": "preview: sentence to say (optional)."},
	},
	"additionalProperties": false,
}
