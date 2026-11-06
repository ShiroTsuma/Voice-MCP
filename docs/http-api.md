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

Answer: `{"name": "Mira", "persona": "…", "messages": [], "others": ["Tobias", "Zofia"]}`. `others` are the other sessions' names, which `send` takes. `persona`, when present, is the character the user gave the session (with **Give sessions characters** on): put it in your system prompt. `speech`, when present, says how much of each reply to say aloud, because the user works by voice (**Says aloud** set to Most of it or Nearly all): put it in your system prompt too, and `say` far more than a sentence or two, a long reply in pieces with one `turn`. Without it, keep speech short.

Register again with the same `session` after voice restarts; it is harmless at any time.

### poll

```json
{"session": "notes-3f2a", "state": "idle"}
```

Poll every second or two while your agent runs. A session that has not polled for 10 seconds counts as gone for dictation, and voice types what the user says into the foreground window instead.

- `"state": "idle"`: the agent waits for the user. `messages` are the user's next prompt; answer them. The turn they start counts as busy until you poll idle again or send `done`.
- `"state": "busy"`: the agent is working. `messages` are what the user said meanwhile ("also check the tests"): take them into account in the running turn.

Answer: `{"name": "Mira", "messages": ["add a dark theme"], "voice_reply": true, "persona": "…", "others": ["Tobias", "Zofia"]}`. `voice_reply` means the user spoke these words and has **Answer by voice** on: say the key point of your answer aloud (`say`), or as much as `speech` says. `persona` appears once after the user changed the session's character, and `speech` once after the user changed how much it says aloud (back to short included): it replaces what you were told before. `"interrupted": true` appears once after the user started dictating while this session spoke or had speech queued: voice has already stopped that speech, so drop the rest of what you meant to say and listen to what comes next.

### say

```json
{"session": "notes-3f2a", "text": "The dark theme is in.", "intent": "info"}
```

Speaks in the session's character's voice and returns at once while it plays: `{"id": "v12", "queue_position": 0}`. `intent` is `info` (default), `question` or `attention`. Keep it to one or two short sentences; details belong on screen, unless `speech` from `register` or `poll` asks for more. Then a line may be up to 4000 characters, and a long reply goes in several `say` calls with the same `turn`.

The rest is optional, as on the MCP's `voice_speak`:

| Field | |
| --- | --- |
| `mood` | how it is said: `neutral`, `happy`, `excited`, `amused`, `teasing`, `sarcastic`, `calm`, `tender`, `sad`, `angry`, `afraid`, `surprised`. A voice whose engine can show moods uses it; the others ignore it (`presets` says which can) |
| `lang` | the language of `text` (`en`, `pl`, ...), when it is not the default |
| `interrupt` | `true` cuts in: stops what is playing and clears the queue first |
| `voice` | for this line only, a character's name or a preset (`presets` lists them) instead of the session's |
| `display` | what the transcript and the reading window show instead of `text`, when it is more than is said: a role-play reply with its narration (`*She leans closer.* "You're late."`) while `text` is only the dialogue. Only `text` is spoken; the reading window highlights the spoken words inside the shown text, and dims what was never heard when the line is cut short. With `turn`, each piece brings its own `display`. |
| `turn` | an id for one reply you send in pieces (say each sentence as your model writes it). A `say` with the same `turn` joins the one before while that is still queued or playing: one utterance, no break between the pieces, one transcript line that grows. The answer then has the first piece's `id` and `"state": "appended"`. The turn closes when you poll `idle`, send `event` `done`, `stop`, say something without this `turn`, or 2 seconds after all of it was heard with nothing new; a later piece starts a new utterance. |

The text can carry voice's own tags, which are never read aloud: `{laugh}`, `{sigh}`, `{cough}` and the other sounds, `{pause}` or `{pause:1.5}`.

### scene

```json
{"session": "notes-3f2a", "scene": {"title": "The last slice", "location": "Kitchen", "time": "Late evening"},
 "lines": [
  {"voice": "narrator", "kind": "narration", "text": "The kitchen was quiet."},
  {"voice": "Clara", "text": "Who ate the other three?", "mood": "amused", "to": "everyone"},
  {"pause": 1},
  {"voice": "Diana", "text": "Don't look at me like that.", "mood": "teasing", "action": "crosses her arms"}]}
```

Says a whole scene in several voices, as the MCP's `voice_scene`, and returns at once with its `id`. Each line has `voice` (a character's name or a preset), `text`, and what the reading window shows without saying it: `kind` (`dialogue`, `narration`, `thought`), `mood`, `action`, `to`; `pause` is the silence after it in seconds, and a line with only a pause is a break. `mode` is `auto` (the default: renders it all first when its voices need GPU models that may not fit together), `play` or `render`. `save_to` (an absolute `.wav` or `.mp3` path) writes it to a file instead and answers once the file is written. `lang` as on `say`.

### status

```json
{"session": "notes-3f2a", "id": "v12"}
```

What became of a `say` or a `scene`: `queued`, `rendering` (with its progress), `playing`, `done`, or why not (`error`, `stopped`, `interrupted`, `muted`), with the voices and engines that said it. Without `id`, the speaker's state: the voice, mute, the queue.

### stop

```json
{"session": "notes-3f2a"}
```

Silences this session's speech now: what is playing, when it is this session's, and everything it has queued. Other sessions' speech goes on. Use it when the user cuts in and the new answer does not exist yet; `interrupt` on the next `say` comes too late for that. The stopped lines end as `stopped` in `status`. The MCP's `voice_control stop` does the same for the calling session; the user's stop hotkey still silences everyone.

### style

```json
{"session": "notes-3f2a", "style": "narrative"}
```

How this session speaks. `short` (the default) is a sentence or two, details on screen; `narrative` is for a story or a role-play: speech is the content, each reply said in full, up to 4000 characters a line. Only this session changes.

### theme, themes

```json
{"session": "notes-3f2a", "theme": "storybook"}
```

This session's look in the user's reading window, for example a story's theme for a role-play; `""` goes back to the user's. Other sessions keep theirs. `themes` (with just the session) lists the installed ones.

### presets

```json
{"session": "notes-3f2a"}
```

The voices: each preset with its engine, and the sounds and moods it can make.

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

Passes a message to another session by its name (one of `others`). It reaches that agent the way the user's dictation does (into its running turn, its poll, Claude Code's mod or Codex's queue) and starts with "Message from Mira (another agent session, not the user):", and ends by saying how to answer (`voice_control send` to Mira; over this API, `send` with `"to": "Mira"`). The transcript shows it as Mira to Tobias, not as the user's words. Answer: `{"to": "Tobias", "delivered": "…"}`; an unknown name is refused with the list of sessions. Two sessions can exchange at most 4 messages a minute, either way; more is refused as a loop (two agents answering each other's answers), and the agent should tell the user instead.

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
