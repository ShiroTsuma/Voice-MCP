# Session API

Any agent can use voice the way Claude Code and Codex do: register a conversation, take what the user dictates to it, say things in its character's voice, and report the end of a turn so voice announces it. The API is plain HTTP with JSON on 127.0.0.1, served by voice's background process. [examples/agent.py](../examples/agent.py) is a complete agent in about 150 lines of standard-library Python, with a local model behind it.

The MCP tools (`voice_speak`, `voice_listen`, `voice_control`) work for any MCP client without this API. The API adds what MCP cannot: dictation that reaches the agent on its own, and announcements without a tool call.

## Finding voice

While voice runs, it writes its address and a token to `%APPDATA%\voice\daemon.json` (`~/.config/voice/daemon.json` elsewhere):

```json
{"addr": "127.0.0.1:53591", "token": "34d0…", "pid": 53568, "version": "v1.1.0"}
```

Send every request as `POST http://<addr>/v1/session/<action>` with the header `X-Voice-Token: <token>` and a JSON body. The port and token change when voice restarts: read the file again when a request fails with a connection error or `403`.

Every answer is `{"result": …}` with status 200, or `{"error": "…"}` with status 422 for a request voice refuses (a missing field, an unknown session) or 500 for a failure. The error text says what to fix.

## A session

A session is one conversation of your agent, named by an id you choose (`"session": "notes-3f2a"`); keep it for the conversation's life. Register it with your process ID: voice uses the process to tell running agents apart and forgets the session when the process ends.

### register

```json
{"session": "notes-3f2a", "pid": 12345, "client": "my-agent", "project": "notes", "name": "Mira"}
```

| Field | |
| --- | --- |
| `session` | required: your id for the conversation |
| `pid` | required: your agent's process ID |
| `client` | your program's name, shown in the settings window (default `agent`) |
| `project` | its working folder's name: the user can dictate "notes: …" to reach it |
| `name` | what the user calls it ("Mira, …"); without it the session takes the next free character |

Answer: `{"name": "Mira", "persona": "…", "messages": [], "others": ["Tobias", "Zofia"]}`. `others` are the other sessions' names, which `send` takes. `persona`, when present, is the character the user gave the session (with **Give sessions characters** on): put it in your system prompt.

Register again with the same `session` after voice restarts; it is harmless at any time.

### poll

```json
{"session": "notes-3f2a", "state": "idle"}
```

Poll every second or two while your agent runs. A session that has not polled for 10 seconds counts as gone for dictation, and voice types what the user says into the foreground window instead.

- `"state": "idle"`: the agent waits for the user. `messages` are the user's next prompt; answer them. The turn they start counts as busy until you poll idle again or send `done`.
- `"state": "busy"`: the agent is working. `messages` are what the user said meanwhile ("also check the tests"): take them into account in the running turn.

Answer: `{"name": "Mira", "messages": ["add a dark theme"], "voice_reply": true, "persona": "…", "others": ["Tobias", "Zofia"]}`. `voice_reply` means the user spoke these words and has **Answer by voice** on: say the key point of your answer aloud (`say`). `persona` appears once after the user changed the session's character.

### say

```json
{"session": "notes-3f2a", "text": "The dark theme is in.", "intent": "info"}
```

Speaks in the session's character's voice and returns at once while it plays: `{"id": "v12", "queue_position": 0}`. `intent` is `info` (default), `question` or `attention`. Keep it to one or two short sentences; details belong on screen.

### event

```json
{"session": "notes-3f2a", "event": "done", "text": "<the agent's last message>"}
```

Lets voice announce the moment the way it does for Claude Code and Codex, with the user's Announcements settings: the project first, the last sentences of `text`.

| `event` | |
| --- | --- |
| `done` | the turn ended; `text` is the agent's last message |
| `question` | the agent waits for an answer; `text` is the question, said as it is |
| `permission` | the agent waits for approval; `tool` names the tool |

Answer: `{"said": "notes. The dark theme is in."}`, or `{"said": "", "quiet": "…"}` with the reason it stayed quiet: voice is muted, announcements are off, or the agent spoke for itself in the last 30 seconds (it already said its answer with `say`).

### send

```json
{"session": "notes-3f2a", "to": "Tobias", "text": "Which recipe did the user pick?"}
```

Passes a message to another session by its name (one of `others`). It reaches that agent the way the user's dictation does (into its running turn, its poll, Claude Code's mod or Codex's queue) and starts with "Message from Mira (another agent session, not the user):". The transcript shows it as Mira to Tobias, not as the user's words. Answer: `{"to": "Tobias", "delivered": "…"}`; an unknown name is refused with the list of sessions. Two sessions can exchange at most 4 messages a minute, either way; more is refused as a loop (two agents answering each other's answers), and the agent should tell the user instead.

### transcript

```json
{"session": "notes-3f2a", "limit": 10}
```

The newest lines of what voice said for this session and what the user dictated to it, oldest first, as `{"columns": ["time", "kind", "session", "project", "text", "outcome"], "rows": [...]}`. `"of": "Clara"` reads another session's lines by name instead.

### end

```json
{"session": "notes-3f2a"}
```

Forgets the session at once (voice also does when the process ends).

## Try it with curl

```powershell
$d = Get-Content "$env:APPDATA\voice\daemon.json" | ConvertFrom-Json
$h = @{ 'X-Voice-Token' = $d.token }
$post = { param($action, $body) Invoke-RestMethod "http://$($d.addr)/v1/session/$action" -Method Post -Headers $h -ContentType 'application/json' -Body ($body | ConvertTo-Json) }
& $post register @{ session = 'demo'; pid = $PID; client = 'powershell'; name = 'Mira' }
& $post say @{ session = 'demo'; text = 'Hello from PowerShell.' }
# Dictate "Mira, hello" with the voice hotkey, then:
& $post poll @{ session = 'demo'; state = 'idle' }
```
