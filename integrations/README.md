# Integrations

Each file here is one entry in voice's Integrations window (settings window → Integrations). voice ships these next to `voice.exe`; it also reads `*.toml` from the `integrations` folder in its data folder (`voice paths` shows where), and a file there with the same `id` replaces the shipped one. A change shows the next time the window opens.

## Fields

```toml
id    = "my-tts"            # unique; defaults to the file name
group = "speech"            # agents, speech, gpu, cloning or listening
order = 50                  # position in its group, lower first
title = "My TTS"
about = "One sentence: what it gives the user."
size  = "1.2 GB"            # optional, shown for downloads
link  = "https://…"         # optional, "More" opens it
```

An integration either names a handler that does the work in voice's code:

| `uses` | Extra fields | Does |
| --- | --- | --- |
| `claude-hooks` | | voice's hooks in Claude Code's `settings.json` |
| `claude-mod` | | the `voice@voice` Claude Code mod |
| `codex` | | Codex notify, hooks and the `$character` skill |
| `session-api` | | lists agents connected through the [session API](../docs/http-api.md) |
| `model` | `model = "kokoro"` | a model from `voice models`: its state and a Download button |
| `sidecar` | `backend = "chatterbox"` | a GPU backend: its state and an Install button (`voice sidecar setup`) |
| `builtin` | | always there, nothing to do |

or describes itself with a check and commands, without `uses`:

```toml
id    = "my-tts"
group = "speech"
title = "My TTS"
about = "A local TTS server for the exec engine."

[check]                      # installed when the path exists, or the command exits 0
path    = "my-tts\\server.exe"
command = ["python", "-c", "import my_tts"]

[[actions]]
id      = "install"
label   = "Install"
when    = "missing"          # missing, installed or always (default)
long    = true               # runs in the background with its output in the window
primary = true
command = ["powershell", "-NoProfile", "-File", "install-my-tts.ps1"]

[[actions]]
id      = "remove"
label   = "Remove"
when    = "installed"
command = ["powershell", "-NoProfile", "-File", "remove-my-tts.ps1"]
```

Commands run in the file's folder, without a console window; their output is the action's log. A file that does not parse shows in the window with the error.

An integration lists what is available; a voice that uses it is a preset in `config.toml` (see `config.example.toml`), for example an `exec` or `http` engine pointing at what the integration installed.
