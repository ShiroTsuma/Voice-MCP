# Integrations

Each file here is one entry in voice's Integrations window (settings window → Integrations). voice ships these next to `voice.exe`; it also reads `*.toml` from the `integrations` folder in its data folder (`voice paths` shows where), and a file there with the same `id` replaces the shipped one. A change shows the next time the window opens.

## Fields

```toml
id    = "my-tts"            # unique; defaults to the file name
group = "speech"            # agents, speech, gpu, cloning or listening
order = 50                  # position in its group, lower first
title = "My TTS"
about = "One short line: what it gives the user."
details = "The full explanation: what it is good at and what not."  # optional, behind the card's [i]
license = "Apache-2.0"      # optional; a non-commercial one says so ("CC BY-NC 4.0, non-commercial use only")
runs_on = "the processor (CPU), on any PC"  # optional: what it needs; see below
size  = "1.2 GB"            # optional, shown for downloads
link  = "https://…"         # optional, "More" opens it
requires = ["python"]       # optional: integrations installed first, in the same job
```

A card shows `about` as its one line under the title; the [i] next to the title holds `details` and the facts: what it runs on, graphics memory, size, languages, sounds and moods, license, and for a `planned` one that it is not in voice yet. A file without `details` (an older one) shows the facts alone. `runs_on` comes from `sidecar/backends.json` (`gpus`) for a `sidecar` backend, whatever the file says, and is "the processor (CPU), on any PC" for a `model` or `builtin` one when the file names none; name it for a `planned` engine without claiming a card nobody has tested ("a graphics card; which ones is not known until it is in voice").

An integration either names a handler that does the work in voice's code:

| `uses` | Extra fields | Does |
| --- | --- | --- |
| `claude-hooks` | | voice's hooks in Claude Code's `settings.json` |
| `claude-mod` | | the `voice@voice` Claude Code mod |
| `codex` | | Codex notify, hooks and the `$character` skill |
| `session-api` | | lists agents connected through the [session API](../docs/http-api.md) |
| `model` | `model = "kokoro"`, or `models = [...]` for a family | a model from `voice models`: its state and a Download button; with `models`, one card for several voices (Piper's Polish ones), each downloaded or removed on its own |
| `sidecar` | `backend = "chatterbox"` | a GPU backend: its state and an Install button (`voice sidecar setup`) |
| `python` | | the Python voice brings for the GPU backends |
| `builtin` | | always there, nothing to do |
| `planned` | `link` | an engine voice may get later: listed as "Coming later", last in its group, with nothing to install; when it is built, its file changes to `sidecar` or `model` |

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
