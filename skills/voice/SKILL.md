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
- One or two short, natural sentences in English, even when the
  conversation is in another language; the voices are English. Pass
  `lang: "pl"` only when the user asks for Polish speech.
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
