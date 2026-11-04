package mcpserver

var listenSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"prompt":       map[string]any{"type": "string", "description": "Optional question to say aloud first; listening starts when it has been spoken."},
		"wait_seconds": map[string]any{"type": "number", "minimum": 3, "maximum": 45, "default": 15, "description": "How long to wait for the user to start talking."},
		"max_seconds":  map[string]any{"type": "number", "minimum": 3, "maximum": 45, "default": 30, "description": "Longest answer to record."},
	},
	"additionalProperties": false,
}
