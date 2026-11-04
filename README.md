<p align="center">
  <img src="docs/images/icon.png" width="96" height="96" alt="voice icon: three bars over a green light">
</p>

<h1 align="center">voice</h1>

<p align="center">
  <strong>Hear your coding agents. Answer them by voice.</strong><br>
  A local MCP server that gives Claude Code and Codex a voice on Windows: agents say when they finish or need you, and you dictate back.
</p>

<p align="center">
  <a href="https://github.com/ShiroTsuma/Voice-MCP/releases/latest"><img src="https://img.shields.io/badge/Download-Windows_x64-8ab4f8?style=flat-square" alt="Download for Windows x64"></a>
  <img src="https://img.shields.io/badge/MCP-stdio_server-8ab4f8?style=flat-square" alt="MCP stdio server">
  <img src="https://img.shields.io/badge/Speech-offline-98c379?style=flat-square" alt="Speech runs offline">
  <img src="https://img.shields.io/badge/Clients-Claude_Code_%7C_Codex-e5c07b?style=flat-square" alt="Claude Code and Codex">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-GPL--3.0-98c379?style=flat-square" alt="GPL-3.0 license"></a>
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#automatic-agent-setup">Automatic agent setup</a> ·
  <a href="#talk-to-your-agents">Talk to your agents</a> ·
  <a href="#characters">Characters</a> ·
  <a href="#add-your-own-voice">Add your own voice</a> ·
  <a href="#updating-from-an-earlier-version">Updating</a> ·
  <a href="#reference">Reference</a>
</p>

<p align="center">
  <img src="docs/images/settings.png" width="380" alt="The voice settings window: Anna is speaking a line that fills in word by word, your last dictated reply below it, and three sessions with their projects and states">
</p>

<p align="center"><em>The settings window, opened from the tray icon. Sessions and lines here are sample data.</em></p>

You run several agent sessions and none of them is on screen. voice gives each one a name and a voice: when a turn ends or an agent needs a decision, you hear it ("shop-api. Two tests fail. Should I fix them now?"). Press <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>D</kbd>, say "Anna, yes, fix them", and the message reaches that session, whether it is busy, idle or in a window you cannot see.

voice is an [MCP](https://modelcontextprotocol.io) server: any MCP client can use its tools to speak (`voice_speak`), ask and listen for a spoken answer (`voice_listen`), control playback and characters and read the transcript (`voice_control`) and clone voices (`voice_clone`); see [MCP tools](#mcp-tools). For Claude Code and Codex it adds hooks and a Claude Code mod, so announcements and dictation work even when the agent does not call a tool. Any other agent gets the same through the [session API](docs/http-api.md), with [examples/agent.py](examples/agent.py) as a starting point.

Everything runs on your PC. Speech synthesis (Kokoro, Piper, Pocket) and recognition (Parakeet, Whisper) run on the CPU through sherpa-onnx; nothing you say leaves the machine. One Go binary that installs itself.

| You want to | voice does |
| --- | --- |
| Know when an agent is done | Hooks announce finished turns and permission prompts with the project and the gist of the last message |
| Answer without switching windows | Dictation goes to the agent you name, or the one that spoke last; a busy agent gets it after its current step |
| Answer a question dialog | A question the agent asks is read aloud with its choices, and what you dictate answers it |
| Keep sessions apart | Each session plays a character with its own name, voice, colour and personality |
| Follow along | A transcript of everything said and dictated, and an optional corner popup while an agent speaks or while you dictate, with your words as they are heard |
| Use your own voice | Clone a voice from a 5 to 15 second recording, from the microphone or a file |
| Use another agent | Any agent, including one on a local model, can register over a small HTTP API, take dictation, speak, announce its turns and message other sessions: [docs/http-api.md](docs/http-api.md) |
| Stay in control | Hotkeys to stop and mute, ducking of other apps, Esc or "cancel" to drop a dictation |

<details>
<summary><strong>More screenshots</strong>: speaking popup, transcript, settings, characters</summary>

<br>

<table>
  <tr>
    <td align="center" valign="top"><img src="docs/images/speaking-popup.png" width="380" alt="The speaking popup in the screen corner"><br><em>The corner popup while an agent speaks. It takes no clicks or focus and stays while you dictate.</em></td>
    <td align="center" valign="top"><img src="docs/images/settings-transcript.png" width="380" alt="The transcript, newest first"><br><em>The transcript, newest first: what each agent said and what you dictated, with where it went.</em></td>
  </tr>
  <tr>
    <td align="center" valign="top"><img src="docs/images/settings-speaking.png" width="380" alt="The Speaking settings group"><br><em>Every setting applies at once. Hotkeys are set by pressing them.</em></td>
    <td></td>
  </tr>
</table>

<img src="docs/images/characters.png" alt="The characters window: a list of characters and the editor for Clara with personality traits, description, voice and colour">

*The characters window: personality traits, a description in your own words, a voice, a colour and an optional picture.*

</details>

## Quick start

**Requirements:** Windows 10 or 11 (x64) with WebView2 (included in Windows 11), Claude Code and/or Codex, a microphone for dictation. About 1 GB of disk for the default models.

1. Download `voice-windows-x64.zip` and `SHA256SUMS` from the [latest release](https://github.com/ShiroTsuma/Voice-MCP/releases/latest). Check the archive (`Get-FileHash .\voice-windows-x64.zip` must match `SHA256SUMS`), then extract it to a permanent folder, for example `%LOCALAPPDATA%\voice`.

2. Close Codex and Claude Code, open a terminal in that folder and run:

   ```powershell
   .\voice.exe install                      # or: .\voice.exe install -data-root D:\voice-data
   ```

   This registers the MCP server in Codex and Claude Code, adds the voice skill and the hooks, and installs the Claude Code mod when the `claude` command is on PATH. `-data-root` keeps the models off the system drive. [What the install changes](#what-the-install-changes) lists every file it touches.

3. Download the voices and the recognizer, and check that you hear it:

   ```powershell
   .\voice.exe models pull runtime kokoro vad asr-parakeet
   .\voice.exe say "Hello. I will tell you when your agents need you."
   ```

   Without the models, voice falls back to the Windows voices.

4. Start Codex and Claude Code again. In Codex, run `/hooks` once and trust voice's hooks.

5. Click the tray icon to open the settings. Turn on **Start at login** in the tray menu if you want the hotkeys ready right after you sign in.

**Windows Defender** sometimes quarantines unsigned Go programs such as `voice.exe` (for example as Wacatac). Compare the archive with `SHA256SUMS` before trusting it, then restore the file and add an exclusion for the folder you extracted it to.

## Automatic agent setup

Paste this prompt into Claude Code or Codex. The agent needs to download files, run local commands and edit its configuration:

```text
Set up voice from https://github.com/ShiroTsuma/Voice-MCP on this Windows PC.
Read the repository README first. Ask me only for what is missing: the
folder to install voice in (default %LOCALAPPDATA%\voice) and, optionally,
a folder for the speech models (a few GB; default inside the install folder).

Download voice-windows-x64.zip and SHA256SUMS from the latest GitHub release
of that repository. Check the archive's SHA-256 against SHA256SUMS before
extracting it. If there is no release or the checksum differs, stop and tell
me; do not build from source or download from anywhere else.

If voice is already installed there, tell me to close Codex and Claude Code
before replacing it, and wait for me. Extract the archive into the install
folder, then run from it:
  voice.exe install [-data-root <models folder>]
Keep all other MCP servers, hooks and settings as they are.

Download the default models and test the voice:
  voice.exe models pull runtime kokoro vad asr-parakeet
  voice.exe say "voice is installed"
and ask me whether I heard it.

Finish by telling me the manual steps: restart my agent sessions, run /hooks
in Codex and trust voice's hooks, and press Ctrl+Alt+D to try dictation.
Do not clone voices, change voice settings or turn on start at login.
```

The agent installs and tests voice. Trusting the Codex hooks, restarting sessions and the first dictation stay with you.

## Talk to your agents

**Hearing them.** voice speaks when an agent finishes a turn, asks for permission or asks you a question. The agent can also speak for itself through the `voice_speak` tool, which its skill tells it to use rarely and briefly. With **Answer by voice** on (Speaking), an agent that you dictated to says the key point of its answer aloud too.

**Answering them.** Press <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>D</kbd>, speak, then pause for two seconds or press the hotkey again. Your words appear in the settings window as you say them; at the end the whole recording is transcribed offline once more and delivered:

- **to the agent you name**: start with its name ("Clara, add a dark theme"), with **Pick the agent by name** on (Dictation);
- otherwise **to the agent that spoke last** or most recently ran a hook.

How it arrives depends on the agent's state:

| The agent is | What happens |
| --- | --- |
| Working (Claude Code or Codex) | voice says "Passing it on" and queues it. The agent gets it after its current step and takes it into account without stopping |
| Idle, Claude Code with the voice mod | The mod submits it as your prompt, on screen or not |
| Idle, Codex | It goes into Codex's own queue (`codex queue`), which the app picks up even when the conversation is not in front |
| Idle, anything else | It is typed into the agent's terminal or window; with **Press Enter** on, it is sent at once |
| Asking you a question (Claude Code) | What you say answers the dialog, as if you had typed it under "Other" |

Press <kbd>Esc</kbd> while dictating to drop the recording. Saying "cancel" alone, or ending with a sentence of its own such as "Scrap it." or "Cancel it.", drops it too; "start over" begins again. Nothing that could not be delivered is lost: it lands on the clipboard and voice tells you so.

## Characters

Sessions take their names from a list of characters (settings window → Characters). Each has a name, personality traits, an optional description in your own words, a voice, a colour for its light and an optional picture. A new session takes the first character no other session uses; the first one (★) is the default.

Change a session's character by right-clicking it in the settings window, with `/character` in Claude Code (`/character Rebekka` switches at once; without a name it asks), or with `$character` in Codex.

With **Give sessions characters** on, the agent plays its character in every reply, written and spoken, and speaks with its voice. The character changes only the tone: the agent still works correctly and keeps helping. Off, the names still route dictation, and agents keep their usual tone and the default voice.

## Add your own voice

A voice comes in three layers, each in `config.toml` next to `voice.exe` (create it from `config.example.toml`):

1. An **engine** makes the sound. Kokoro, Piper, Windows SAPI and the GPU backends (Chatterbox, XTTS, F5) are built in. Any program that writes a WAV file can be another one (`type = "exec"`), and so can any server that speaks voice's HTTP protocol (`type = "http"`).
2. A **preset** picks an engine and tunes it: voice, speed, pitch, effects, a different engine for Polish. The built-in `calm-mature` is Kokoro's `af_nicole`, slower and lower.
3. A **character** speaks with a preset. Pick it in the Characters window (Voice), or set `preset` in its `[[characters]]` entry.

For example, Chatterbox's own voice for one character, and a program of your own for another:

```toml
# 2. A preset on a built-in engine (install Chatterbox in Integrations first).
[presets.rebekka-voice]
engine = "chatterbox"
description = "Chatterbox, English and Polish"

# 1. An engine of your own: any command that turns text into a WAV.
#    Placeholders: {text} {voice} {lang} {speed} {wpm} {out}; without {text}
#    the text goes to stdin, with {out} the WAV is read from that file.
[engines.my-tts]
type = "exec"
command = 'D:\tools\my-tts\my-tts.exe'
args = ["--text", "{text}", "--out", "{out}"]

[presets.clara-voice]
engine = "my-tts"
description = "My own TTS"
speed = 0.95
dsp = ["compress:soft", "reverb:room"]

# 3. Characters with those presets.
[[characters]]
name = "Rebekka"
traits = ["teasing", "playful"]
preset = "rebekka-voice"

[[characters]]
name = "Clara"
traits = ["calm", "formal"]
preset = "clara-voice"
```

`voice presets` lists the presets with their engines, and **Hear it** in the settings window plays one. Cloning makes a preset for you: `voice clone add me clip.wav -engine chatterbox` creates `clone-me`. More engine and preset fields are in [config.example.toml](config.example.toml).

**Show it in the Integrations window.** When your engine has something to install, a TOML file in the data folder's `integrations` folder (`voice paths` shows where) puts it in the window, with its state and the commands behind its buttons:

```toml
id    = "my-tts"
group = "speech"
title = "My TTS"
about = "My own text-to-speech for the Clara voice."
[check]
path = 'D:\tools\my-tts\my-tts.exe'
[[actions]]
id = "install"
label = "Install"
when = "missing"
long = true
primary = true
command = ["powershell", "-NoProfile", "-File", 'D:\tools\my-tts\install.ps1']
```

The window lists what can be installed; the preset in `config.toml` is what makes a character use it. [integrations/README.md](integrations/README.md) has every field, the built-in handlers and dependencies between integrations (`requires`).

## Updating from an earlier version

Close Codex and Claude Code, extract the new release over the old folder (your `config.toml`, `settings.toml`, models and voices stay), then run `.\voice.exe install` again. It stops the running copy first and redoes the client setup. After it:

**Codex**

1. Start Codex and run `/hooks`: trust voice's five hooks (`voice.exe hook codex-hooks`). Codex skips a hook until it is trusted, and asks again when a voice update changes one.
2. Restart every Codex session that was already open: a session reads its hooks and skills when it starts.
3. Check: give Codex a longer task and dictate to it while it works; it mentions your message during the turn. Dictated while it is idle, the message reaches it through `codex queue`, on screen or not. `$character` lists the characters; `$character Rebekka` switches.

**Claude Code**

1. Check that the mod is in: `claude plugin list` shows `voice@voice`, enabled. With only the Claude app (no `claude` command on PATH) it is not installed; install it by hand with the two commands in [claude-plugin/voice-dictation](claude-plugin/voice-dictation/README.md).
2. Start new sessions: the mod, its status line (the session's name) and `/character` load only in sessions started after the update.
3. Check: dictate to an idle session that is not the one on screen ("Annie, …"); it answers on its own. `/character` opens the picker.

**Both**

- If an announcement plays twice after a turn, run `.\voice.exe install` again: an update of the Codex app may wrap voice's notify inside its own, and voice unwraps it.
- `serve.log` in the data folder shows where each dictation went (`dictation: queued … for Annie (its Claude Code mod submits them)`, `… in Codex's queue`, `… busy (its next hook passes them on)`).

---

## Reference

- [What the install changes](#what-the-install-changes)
- [How it runs](#how-it-runs)
- [Dictation in detail](#dictation-in-detail)
- [Commands](#commands) and [MCP tools](#mcp-tools)
- [Registering by hand](#registering-by-hand)
- [Voices](#voices), [Cloning](#cloning), [Listening](#listening)
- [GPU sidecar](#gpu-sidecar)
- [Storage](#storage)
- [Building from source](#building-from-source)

### What the install changes

`voice install` works from the folder voice.exe is in. It:

- creates `config.toml` from `config.example.toml` when there is none, and with `-data-root DIR` writes `data_root` into it, so models and the sidecar live off the system drive;
- Codex (when `~/.codex/config.toml` exists): an `[mcp_servers.voice]` entry running `voice.exe mcp`, with `tool_timeout_sec = 120` for `voice_listen`; the voice skill in `~/.codex/skills/voice`; `notify` = `voice hook codex`, chaining the previous notify command (announcements); SessionStart, UserPromptSubmit, PostToolUse, Stop and Interrupt hooks in `~/.codex/hooks.json` (dictation); the `$character` skill in `~/.codex/skills/character`;
- Claude Code (when `~/.claude` exists): the MCP server at user scope (`claude mcp add`, when the `claude` command is on PATH; otherwise it prints the command); the voice skill in `~/.claude/skills/voice`; Notification, Stop, SessionStart, UserPromptSubmit and PostToolUse hooks in `settings.json`, replacing earlier voice entries (the last three register the session and pass dictation on, silently); the `voice` mod (`voice@voice`, from `claude-plugin\voice-dictation`), which starts a turn with dictation in an idle session, answers question dialogs and adds `/character`.

Each client is set up only when it is installed; for one installed later, run `voice install` again. `voice uninstall` stops voice, takes it out of both clients (puts back the Codex `notify` command it chained), removes its hooks, skills and the mod, and turns off start at login; delete the folder afterwards to remove the models and settings.

voice also runs behind a shared MCP gateway (`voice setup` is the hook an installer calls); see [Registering by hand](#registering-by-hand).

### How it runs

One background process, `voice serve`, owns the audio device, the models and the speech queue. MCP servers, hooks and the CLI are thin clients that start it on demand (an MCP server starts it as soon as its client connects). That gives:

- one persistent **voice** entry with an icon in the Windows volume mixer, where its volume can be set or muted and Windows remembers it;
- a **tray icon**: switch voice, mute, stop, ducking and barge-in, open the settings window, folders and the log, restart or quit;
- a **settings window** (click the tray icon): a dark flyout above the tray with who is speaking, your last dictation, the sessions, the transcript and the settings used every day. **All settings** at its bottom opens the full window: every setting by section (voice tuning, keys, dictation details, cloning, recognition), with Characters and Integrations. Changes apply at once and go to `settings.toml`, which overlays your `config.toml`. It closes when you click elsewhere or press Esc. It renders with WebView2;
- an optional **speaking popup** (Speaking → Show who speaks): the top of the settings window in the screen corner while an agent speaks, for 4, 8 or 12 seconds after; it never takes focus or clicks, and appears too when you close the settings window mid-sentence;
- **hotkeys**: stop speaking (`Ctrl+Alt+End`), mute (`Ctrl+Alt+M`) and dictation (`Ctrl+Alt+D`);
- **ducking**: other apps play at 35% while voice speaks;
- optional **barge-in**: voice stops when you start talking (headphones);
- optional **start at login** (tray → Start at login, or `voice autostart on`): a shortcut in the Startup folder, so the tray and hotkeys are there right after you sign in;
- idle models are freed after 15 minutes and the GPU sidecar exits after 10 minutes without requests; both come back on the next use.

`voice serve status|stop` and `voice mixer` (session volumes) help diagnose.

### Dictation in detail

voice knows the agents: the shared-mcp gateway passes each agent's process ID with its calls, and the hooks pass theirs. The recognizer is `[listen] model` (Parakeet handles Polish and English).

- **The agent is working** (Claude Code between its `UserPromptSubmit` and `Stop` hooks, Codex between its hooks): voice queues the message. The next hook hands it over: after the current tool call as context ("the user just said ..."), or at the end of the turn, which then goes on to answer it.
- **The session runs voice's Claude Code mod**: the message waits in voice, and the mod submits it as your prompt once the session is idle, on screen or not. Nothing is typed. The status line shows the name you dictate to the session by (🎙 Annie). The mod polls voice on 127.0.0.1 every 1.5 s, with the address and token voice writes to `%APPDATA%\voice\daemon.json` while it runs; see [claude-plugin/voice-dictation](claude-plugin/voice-dictation/README.md). While a question dialog (AskUserQuestion) is open, the mod reads the question aloud and what you dictate answers it; a click still works.
- **Codex** gets the same through its own hooks, which `voice setup -clients` adds to `~/.codex/hooks.json` (`voice hook codex-hooks`): a running turn takes what you dictate as context, and its end continues with it. Codex runs a hook only once you trust it (`/hooks`). An idle Codex gets the message through `codex queue`, which the Codex app (or CLI) holding the conversation picks up and answers. The Codex app runs all its conversations in one process, so voice reaches the one that was active last.
- **Anything else idle**: voice types the message into its input. Claude Code or Codex in their desktop app: the app's window comes to the front and gets the text. In a terminal (Windows Terminal, a console, VS Code's terminal): into the agent's own console input, whatever window is in front.
- **Nothing took it**: the clipboard, and voice says so.

Typing waits until you let go of Ctrl and Alt, so ending the recording with the hotkey does not turn the text into shortcuts. `[serve] dictate_submit = true` presses Enter after typed text. `dictate_target = "console"` only uses terminal agents, `"window"` only the window in front. A server cannot start the user's turn over MCP, which is why voice types, hands over through hooks, or leaves it to the mod. `voice dictate "text"` delivers text the same way, to try it without speaking.

Characters are `[[characters]]` in `config.toml`: `name`, `traits = [...]`, `description`, `preset`, `color` and `image` (cropped to a square, kept in `data\avatars`). `[serve] personas = true` turns on **Give sessions characters**. Claude Code learns its character from its SessionStart hook, Codex from `voice_control register`; a character changed in the settings reaches Claude Code with its next hook and Codex with its next voice call, and an agent can switch itself with `voice_control set_character` when you ask (`list_characters` shows the list).

### Commands

| Command | Purpose |
|---|---|
| `voice mcp [-preset p]` | MCP server on stdio |
| `voice say [-preset p] [-intent info\|question\|attention] [-lang en] [-out f.wav] text` | speak (or render) text |
| `voice hook claude` / `voice hook codex <json>` | announce client events; always exits 0 |
| `voice listen [-wait 15s] [-max 30s] [-json]` | record an answer and print the transcript |
| `voice dictate text` | deliver text as if it were dictated |
| `voice transcribe [-model dir] f.wav` | speech to text for a file |
| `voice clone record\|add\|list\|preview\|text\|remove` | cloned voices |
| `voice sidecar setup [-gpu ...] backend,...` | GPU voice backends (`--help` changes nothing) |
| `voice presets`, `voice voices -engine e`, `voice devices` | inspect |
| `voice models [pull name\|all]` | download runtime and models |
| `voice paths` | every data location with its size |
| `voice setup -clients` | redo the Codex and Claude Code setup |

### MCP tools

- `voice_speak(text, intent, lang, interrupt)` queues speech and returns at once.
- `voice_control(action: status|stop|mute|unmute|list_presets|set_preset|register|list_characters|set_character|transcript, ...)`. `transcript` returns the newest lines of what voice said and what the user dictated, with the session each was for and where it went; `session` filters by a session's or character's name (or `me`), `limit` sets how many (20 by default, 50 at most). The last 50 lines are kept in `transcript.json` in the data folder, so they survive a restart.
- `voice_listen(prompt, wait_seconds, max_seconds)` optionally asks aloud, then records until the user pauses and returns `{"text","lang","seconds"}`. The microphone is open only during the call, between two chimes, and no voice process speaks meanwhile.
- `voice_clone(action: record|add|list|preview|set_transcript|remove, ...)` creates a voice from the microphone (`record`, `seconds`) or a clip (`add`); both refuse without `rights_confirmed=true`, which the agent may only pass after the user confirms they may use that voice.

Server instructions tell agents to speak rarely and briefly, in English.

### Registering by hand

`mcp-install` does this for you. Run the server behind `shared-mcp` so all sessions share one backend (one model in memory, one speech queue). `mute` and `set_preset` are then global; register with `-preset` per client for different voices (that starts separate backends; a cross-process lock still keeps them from talking over each other).

Claude Code:

```powershell
claude mcp add voice --scope user -- "<dir>\shared-mcp.exe" --idle-seconds 300 -- "<dir>\voice.exe" mcp
```

Codex (`~/.codex/config.toml`):

```toml
[mcp_servers.voice]
command = '<dir>\shared-mcp.exe'
args = ["--idle-seconds", "300", "--", '<dir>\voice.exe', "mcp", "-preset", "narrator"]
tool_timeout_sec = 120   # voice_listen waits for the user
```

Hooks announce the important moments: "voice. Done. The installer now registers voice in both clients." or "shop. I need your permission to use Bash." They say the project and the last sentences of the agent's message (Announcements sets how much), and stay quiet when muted, right after the agent spoke for itself, and (Claude `Stop`) after quick turns. Each client can have its own voice via `[clients]`: the shared-mcp gateway passes the calling client's name (`claude-code`, `codex-mcp-client`) with every call, and `voice mcp -client <name>` fixes it.

Claude Code (`~/.claude/settings.json`):

```json
"hooks": {
  "Notification": [{ "hooks": [{ "type": "command", "command": "\"<dir>\\voice.exe\" hook claude" }] }],
  "Stop":         [{ "hooks": [{ "type": "command", "command": "\"<dir>\\voice.exe\" hook claude" }] }]
}
```

Codex (`~/.codex/config.toml`, top level):

```toml
notify = ['<dir>\voice.exe', "hook", "codex"]
```

### Voices

Presets combine an engine, voice, speed, pitch, effects and per-language overrides; see `config.example.toml`. Built in:

| Preset | Voice |
|---|---|
| `neutral` (default) | Kokoro `af_heart` |
| `calm-mature` | Kokoro `af_nicole`, slower, lower, soft compression and room |
| `narrator` | Kokoro British `bm_george` |
| `brisk` | neutral, faster, no chime |
| `system` | Windows SAPI, no downloads |
| `clone-<name>` | one per cloned voice |

English is the default language (`default_lang`); the Polish Piper voices are used only when a request asks for `pl`.

Engine types: `sherpa` (Kokoro, Piper/VITS, Pocket, ZipVoice in process), `http` (sidecar or any server speaking its protocol), `exec` (any program that writes WAV; `{ref}` and `{ref_text}` placeholders for cloning) and `sapi`.

### Cloning

In the settings window: Voice clone → pick the engine (the installed ones first: Pocket on the CPU for English, Chatterbox or XTTS on the GPU for Polish too), then record from the microphone or drop a sound file. From the command line:

```powershell
voice clone record me -rights "my own voice"      # or: voice clone add me D:\clips\me.wav ...
voice clone preview me
voice say -preset clone-me "Hello."
```

`record` counts down with three ticks, opens the microphone with a chime for 12 s (`-seconds 6..30`) and closes it with another; read a few natural sentences aloud. The recording is checked before it is used: almost silent or heavily clipped audio is refused with what to fix; quiet, slightly clipped or noisy audio is added with a warning. The clip is converted to mono 24 kHz, trimmed, capped at 15 s and normalized; 5-15 s of one clear speaker works best. Non-WAV input needs `ffmpeg` on PATH. When a recognition model is installed the clip is transcribed automatically (ZipVoice and F5 need the transcript). The default engine is Pocket (CPU, English); pass `-engine chatterbox` or `xtts` for Polish once the sidecar is set up.

Only clone a voice you have the right to use.

### Listening

`[listen] model` picks the recognizer: Parakeet TDT v3 (default; fast, 25 European languages) or Whisper turbo (about 100 languages, several times slower on CPU). Silero VAD ends the recording after `end_silence` seconds of quiet.

Recognizers mishear names they have never heard. voice corrects the text afterwards, with any recognizer: a word that sounds like a character's or session's name, or a project named like an identifier (`MCP-Tools`, `isaac64`), is written as that name ("Rebecca" becomes "Rebekka", "MCP tools" becomes "MCP-Tools"). Add your own words under Listening and memory → Words to get right (`[listen] words` in `config.toml`), one per line; `heard -> meant` replaces what the recognizer keeps writing wrong (`rytmi -> README`). Dictation, its live words and `voice_listen` all use it.

### GPU sidecar

```powershell
voice sidecar setup --help          # changes nothing
voice sidecar setup chatterbox      # also: f5, xtts; -gpu auto|nvidia|amd|cpu
voice clone add me clip.wav -engine chatterbox -rights "..."
```

The easiest way is the Integrations window (settings window → Integrations → GPU voices): Install there first downloads a Python 3.12 that voice brings along (python-build-standalone, pinned by SHA-256, into `sidecar_dir\python`), so you need no Python of your own. From the command line, setup needs Python 3.12 (3.10-3.12 without AMD), or `-python <voice's python.exe>`. It looks for it through `py`, the standard install folders and PATH, and says so before installing anything if none fits (`-python C:\path\python.exe` picks one). Each backend gets its own environment in `sidecar_dir\envs\<backend>`, so one backend's pins (chatterbox pins an old torch) cannot break another; setups from before keep working from the shared `sidecar_dir\.venv`. Downloads are cached in `sidecar_dir\.pip-cache` and finished stages are recorded, so a rerun after an interruption resumes (`-force` redoes them). A GPU computation test checks PyTorch before the backend is installed. The `chatterbox`, `f5` and `xtts` engines start their server on first use, log to `sidecar\<engine>.log`, keep model weights in `sidecar\models` and stay running for later calls. Other models plug in as a Python module with a `Backend` class (see `sidecar/voice_sidecar/backends/__init__.py`) started with `--backend your.module`.

GPUs: NVIDIA gets CUDA PyTorch (`-cuda cu128` default, needed for RTX 50). AMD gets AMD's ROCm PyTorch for Windows from a profile in `sidecar\profiles` (`amd-rocm-windows`: ROCm 7.2.1, PyTorch 2.9.1, Python 3.12, for Radeon RX 9070/9070 XT/9060 XT, RX 7900 XTX, RX 7700 and AI PRO R9700; driver 26.2.2 or newer). A newer ROCm release from [AMD's install page](https://rocm.docs.amd.com/projects/radeon-ryzen/en/latest/docs/install/installrad/windows/install-pytorch.html) goes in as another profile (`-profile file.json`) or with `-torch-wheels`. F5-TTS needs `torch.distributed`, which AMD's Windows build lacks: setup checks that and skips f5 before downloading it. Tested pins for a backend go in `sidecar\requirements\<backend>-<gpu>.txt` (used as pip constraints). Everything outside the sidecar (Kokoro, Piper, Pocket cloning, Parakeet/Whisper listening) runs on the CPU and works the same on AMD and NVIDIA.

Model licenses vary (Pocket, F5 weights and XTTS are non-commercial); check them before any commercial use.

### Storage

Models (~3 GB with everything) and the GPU sidecar (PyTorch plus weights, 5-10 GB per backend) add up. By default data lives next to `voice.exe`; move it off a full system drive in `config.toml`:

```toml
data_root = 'D:\voice-data'     # models, runtime, voices, data, sidecar below it
# or one part at a time; relative values are relative to data_root
sidecar_dir = 'E:\big\voice-sidecar'
```

`voice paths` prints every location with its size. The sidecar's pip downloads, build files and Hugging Face weights also stay in `sidecar_dir`. Only `config.toml` and the small `sidecar\voice_sidecar` code stay next to `voice.exe`.

### Building from source

Needs Go (the version in `go.mod`) on PATH; nothing else for the product build.

```powershell
.\build.ps1 -Test                       # bin\voice.exe, after go vet and go test
bin\voice.exe models pull runtime kokoro vad asr-parakeet
bin\voice.exe say -intent question "All done. Should I commit?"
.\scripts\package-release.ps1 -Version v1.0.0   # dist\voice-windows-x64.zip and SHA256SUMS
```

A release is an annotated tag (`git tag -a v1.0.1 -m "notes"`); pushing it runs `.github/workflows/release.yml`, which builds, tests and publishes the archive.

```
cmd/voice            CLI, MCP entry, tray, hotkeys, dictation delivery, install
internal/core        queue, cleanup, sentence pipelining, playback lock, cache
internal/engine/...  sherpa (DLL via syscall), httpengine, command, sapi
internal/audio       winmm output and capture, WAV, resampling, effects
internal/listen      microphone + VAD + recognizer, live phrases
internal/clones      cloned voice profiles
internal/hooks       Claude Code / Codex hook parsing and announcements
internal/mcpserver   MCP tools
internal/settingsui  settings window and characters window (HTML)
claude-plugin/       the Claude Code mod
sidecar/             Python sidecar
skills/voice         the skill agents get
```

## License

GPL-3.0-only; see [LICENSE](LICENSE). The speech models have their own licenses, and `THIRD_PARTY_NOTICES.txt` in the release lists the Go dependencies'.
