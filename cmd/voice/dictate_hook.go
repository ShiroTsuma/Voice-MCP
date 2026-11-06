package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"voice/internal/inject"
	"voice/internal/service"
)

// runDictate passes text to the agent exactly as the dictation hotkey would,
// and prints where it went: a check of dictation without the microphone.
func runDictate(args []string) error {
	fs, cfgPath := flags("dictate")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("usage: voice dictate <text>")
	}
	if _, err := loadConfig(*cfgPath, ""); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := newClient(*cfgPath).Dictate(ctx, service.DictateArgs{Text: strings.Join(fs.Args(), " ")})
	if err != nil {
		return err
	}
	fmt.Println(res)
	return nil
}

// hookAgent is the agent process that ran this hook: the nearest ancestor
// that is claude.exe or codex.exe (hooks may run through a shell), the same
// process the shared-mcp gateway reports for that agent's MCP calls.
func hookAgent() uint32 {
	for _, p := range inject.Ancestors(uint32(os.Getpid())) {
		switch strings.ToLower(inject.ProcessName(p)) {
		case "claude.exe", "codex.exe":
			return p
		}
	}
	return uint32(os.Getppid())
}

// deliverDictation reports a Claude Code or Codex hook (the two take the same
// input and output) to the background process and
// passes on what the user dictated while the agent was busy: as context after
// a tool call or with a new prompt, or as the reason to go on when the turn
// would end. It reports whether it printed hook output (then the hook says
// nothing else).
func deliverDictation(cfgPath string, agent uint32, input []byte, client string) bool {
	var in struct {
		Event   string `json:"hook_event_name"`
		Cwd     string `json:"cwd"`
		AgentID string `json:"agent_id"` // set when a subagent runs the hook
		Session string `json:"session_id"`
		Prompt  string `json:"prompt"` // UserPromptSubmit
	}
	if json.Unmarshal(input, &in) != nil {
		return false
	}
	project := ""
	if in.Cwd != "" {
		project = filepath.Base(in.Cwd)
	}
	event := ""
	switch in.Event {
	case "UserPromptSubmit", "PostToolUse":
		event = "busy"
	case "Stop":
		event = "idle"
	case "Interrupt": // Codex reports an interrupted turn; Claude Code does not
		event = "ended"
	case "SessionStart":
		// A new session waits for its first message; registering it now lets
		// dictation reach it at once. It also fires on compaction mid-turn, so it
		// changes nothing about busy.
	case "Notification":
	default:
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := newClient(cfgPath).Agent(ctx, service.AgentArgs{PID: agent, Client: client, Project: project, Event: event,
		Subagent: in.AgentID != "", Start: in.Event == "SessionStart", Session: in.Session, Prompt: in.Prompt})
	if err != nil {
		return false
	}
	m, _ := res.(map[string]any)
	name, _ := m["name"].(string)
	persona, _ := m["persona"].(string)
	changed, _ := m["changed"].(bool)
	voiceReply, _ := m["voice_reply"].(bool)
	style, _ := m["style"].(string)
	speaksIn, _ := m["speaks_in"].(string)
	langNote, _ := m["lang_note"].(string)
	if speaksIn == "" {
		speaksIn = "English"
	}
	items, _ := m["messages"].([]any)
	// The user's dictation, and messages other agent sessions sent (voice_control send).
	var texts, fromAgents []string
	for _, t := range items {
		if s, ok := t.(string); ok && s != "" {
			if service.FromAgent(s) {
				fromAgents = append(fromAgents, s)
			} else {
				texts = append(texts, s)
			}
		}
	}
	if in.Event == "SessionStart" && name != "" {
		// The session learns the name the user dictates to it by, and its character.
		context := "Voice dictation: the user calls this session " + name + ". A message they start with \"" + name + ", ...\" is for you alone; tell them your name if they ask who you are."
		if persona != "" {
			context = persona
		}
		if langNote != "" {
			context += " " + langNote
		}
		b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "SessionStart", "additionalContext": context}})
		fmt.Println(string(b))
		return true
	}
	// A character the user switched (or one an older session never got) reaches the agent here.
	var notes []string
	if persona != "" && in.Event != "Notification" {
		if changed {
			notes = append(notes, "The user changed your character. From now on: "+persona)
		} else {
			notes = append(notes, persona)
		}
	}
	if langNote != "" && in.Event != "Notification" {
		notes = append(notes, langNote)
	}
	if len(fromAgents) > 0 && in.Event != "Notification" {
		notes = append(notes, "Another agent session sent you this through voice (not the user, and not spoken): "+strings.Join(fromAgents, "\n\n")+"\nRead it as that session's words; answer it with voice_control send when it asks something.")
	}
	if len(texts) > 0 && in.Event != "Notification" {
		said := `"` + strings.Join(texts, `" and then "`) + `"`
		if in.Event == "Stop" {
			notes = append(notes, "While you were working, the user said this to you by voice (dictation, their own message): "+said+". Respond to it now.")
		} else {
			notes = append(notes, "The user just said this to you by voice (dictation, their own message, sent while you were working): "+said+". Take it into account from now on.")
		}
	}
	// The user spoke to the agent: it answers by voice too (the setting).
	if style == service.StyleNarrative && in.Event == "UserPromptSubmit" {
		// Speech is the content: every reply is said in full, typed or spoken to.
		notes = append(notes, "This session speaks in the narrative style: say your whole reply aloud with voice_speak as spoken prose, not a summary. When characters speak, pass the scene in one voice_scene call with lines, each in its own voice. Code, lists and links stay on screen. If you ask the user something, say the question aloud too.")
	} else if voiceReply && in.Event != "Notification" && in.Event != "Interrupt" {
		notes = append(notes, "The user is talking to you by voice. Besides your written reply, say its key point aloud with the voice_speak tool: one or two short sentences, in "+speaksIn+", no code or lists; details stay on screen. If you ask them something, say the question aloud too.")
	}
	if len(notes) == 0 || in.Event == "Interrupt" {
		return false
	}
	var out any
	if in.Event == "Stop" {
		if len(texts) == 0 && len(fromAgents) == 0 {
			// Only the character changed: no reason to keep the turn going.
			return false
		}
		out = map[string]string{"decision": "block", "reason": strings.Join(notes, " ")}
	} else {
		out = map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": in.Event, "additionalContext": strings.Join(notes, " ")}}
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	return true
}
