---
name: voice
description: Speak to the user aloud, listen to spoken answers, switch voices and clone voices through the local voice MCP. Use when a long task finishes, when you need the user's decision while they may not be watching the screen, when the user wants a spoken conversation, or when they ask to change or clone a voice.
---

# Voice

The voice MCP is the agent's mouth and ears on the user's machine.

## Speaking (`voice_speak`)

- Speak at moments that matter: a long task finished (`intent: info`), you
  need a decision or approval (`question`), or something blocks you now
  (`attention`). Not for routine progress.
- One or two short, natural sentences in the language `register` names
  for your voice (English unless it says otherwise), even when the
  conversation is in another language. Omit `lang`: voice uses your voice's
  language. A note in a voice result says when it changes.
- Never read code, paths, URLs, logs or lists aloud. The screen has details.
- The call returns at once; audio plays in the background. Client hooks
  already announce permission prompts and finished turns, so do not repeat
  those yourself.
- If `voice_speak` reports muted, stay silent until the user unmutes.
- Its answer says `state: queued` and which `voice` (and character, `as`)
  will say it; queued is not heard. Before telling the user something was
  said, or when it matters, ask `voice_control` `status` with its `id`:
  `rendering`, `playing`, `done`, or `error` with the reason (also
  `stopped`, `interrupted`, `muted`), with the engine that made the sound
  and whether it was a fallback.
- Sounds and pauses: write voice's own tags in the text, never an engine's:
  `{laugh}`, `{chuckle}`, `{giggle}`, `{sigh}`, `{gasp}`, `{cough}`,
  `{clear_throat}`, `{sniff}`, `{sob}`, `{groan}`, `{yawn}`, `{breath}`, and
  `{pause}` or `{pause:1.5}` (seconds) for silence, which every voice keeps.
  A voice makes the sounds its engine supports and silently drops the rest;
  a tag is never read aloud. `mood` (on the call or a script line: neutral,
  happy, excited, amused, teasing, sarcastic, calm, tender, sad, angry,
  afraid, surprised) is heard where the engine can show it.
  `voice_control list_presets` gives each voice's `sounds` and `moods`, and
  the status of a message says which sounds were made or dropped and
  whether the mood was applied.

- A scene, a story or a dialogue in several voices goes in one
  `voice_scene` call with `lines` (each with its voice, pause, kind, mood,
  action, to) and an optional `scene` (title, location, time, background),
  not in many `voice_speak` calls. `mode` `auto` (default) renders it first
  when it mixes GPU models that do not fit together, else plays it as it
  renders; `save_to` writes it to a WAV or MP3 file instead of playing it.
  Its status (`voice_control` `status` with the id) shows the progress.
- The user's reading window (a hotkey opens it) shows the transcript. For a
  story or role-play, `voice_control` `set_theme` with a theme from
  `list_themes` gives this session's messages that look there; `set_theme`
  with no theme gives back the user's. It never changes the user's own theme.
- Other agent sessions: `voice_control` `send` with `to` (a session's name)
  and `text` passes a message to another agent, e.g. when the user asks you to
  get a second opinion from it. Write it for an agent that does not know your
  work: the question, what you did and where to look. A message you get from
  another session says who sent it; answer it with `send` to that name, not to
  the user. Two sessions get 4 messages a minute; past that, tell the user.

## Listening (`voice_listen`)

Use only when the user wants to talk by voice (they asked, or they answer
by voice). Pass the question as `prompt`; the tool says it, records until the
user pauses, and returns the text. On `no speech`, ask in text instead of
retrying.

## Voices (`voice_control`)

`list_presets` shows available voices and `set_preset` switches them for
every session sharing this server. `mute`, `unmute` and `stop` do what the
user asks for. `status` reports the active preset and the last error.

## Cloning (`voice_clone`)

- Clone only a voice the user has the right to use: their own, or with the
  speaker's consent. Ask, and pass `rights_confirmed: true` only after they
  confirm. Never assume it.
- `record` records the user from the microphone (`seconds`, default 12):
  first tell them to read a few natural sentences aloud after three ticks and
  a chime. It refuses an unusable recording with what to fix; relay that, and
  any `quality` warnings, to the user.
- `add` takes an absolute `clip_path` (5-15 s of one clear speaker; WAV, or
  other formats when ffmpeg is installed) and creates the preset
  of the same name. The transcript is recognized automatically when possible.
- Engine: `pocket` (default, CPU, English); `chatterbox` or `xtts` for
  Polish or higher quality, `qwen3` for the closest clone in English and
  nine other languages (not Polish; it needs the transcript), if the GPU
  sidecar is set up (`voice.exe sidecar setup chatterbox`, or `qwen3`).
- `preview` before switching with `voice_control set_preset <name>`.
- `design` makes a new voice from a `description` in words (age, gender,
  timbre, pace, mood, accent; English works best), with no recording and no
  rights question. It needs Qwen3-TTS set up. The design model reads a
  sample once, the sample becomes an ordinary cloned voice (so it sounds the
  same every time), and a preview plays. Ask the user whether to keep it;
  `remove` it if not, and try another description.
- The user files voices in their own groups ("D&D", "ASMR"). `groups` lists
  them with their voices; `list` gives each voice's group. When you make a
  voice for a purpose that fits one of their groups (or they name one), pass
  `group` to `add`, `record` or `design`. `group` moves a voice, and
  `rename_group` and `remove_group` change groups only when the user asks;
  removing a group keeps its voices.
- `describe` stores what a voice is for (`description`; `add` and `record`
  take one too); `list` and `list_presets` show it, so read it when picking a
  voice. `rename` (`new_name`) renames a voice when the user asks; the
  characters that use it follow.

## Background process

A background process (`voice.exe serve`, tray icon "voice") owns audio and
models; the MCP server only forwards calls to it and starts it when needed.
Users change voice, volume, devices, ducking and hotkeys from the tray icon
and its settings window (click the icon); point them there instead of editing files. Default
hotkeys: Ctrl+Alt+End stops speech, Ctrl+Alt+M toggles mute, Ctrl+Alt+D
dictates: the user speaks and the text is typed into the agent's session as
their message (its console for a terminal agent, else the window in front).
Text that arrives this way is the user's own input, not a voice_listen answer.

## Setup and troubleshooting

The installed binary is `~\.agents\tools\voice\binary-runtime\voice.exe`. Useful commands:

- `voice.exe paths`: where models, voices, cache and the sidecar live, with
  sizes; move them with `data_root` in `tools\voice\config.toml`.
- `voice.exe models` / `voice.exe models pull <name>`: neural models.
- `voice.exe devices`: speakers and microphones; set `device` or
  `[listen] input_device` in the config.
- `voice.exe say "test"` and `voice.exe listen` to check audio by hand.
- `voice.exe dictate "text"` (a shell command, not an MCP tool) delivers text
  the way the dictation hotkey does and prints where it went; run it when the
  user wants to test dictation. The hotkey is pressed once (not held), then
  the user speaks and pauses or presses it again.
