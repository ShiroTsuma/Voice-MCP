# Adding a GPU speech engine

voice runs large speech models in its Python **sidecar**: one small HTTP server per backend, in its own Python environment, started on first use and stopped after ten idle minutes so the graphics card is free again. A new model needs four things. Nothing else in voice has to change: the setup, the Integrations window, the clone engine list, the command line and the MCP tool all read the same list.

## 1. An entry in `sidecar/backends.json`

```json
{
  "name": "mytts",
  "label": "My TTS",
  "note": "what it speaks and where it runs, in one line (shown in the windows and to agents)",
  "pip": ["mytts-package"],
  "module": "mytts",
  "port": 8769,
  "clones": true,
  "transcript": true,
  "extra": { "temperature": 0.7 },
  "option": {
    "key": "model",
    "label": "Model size",
    "default": "org/mytts-large",
    "choices": [
      { "value": "org/mytts-large", "label": "Large", "note": "better; 8 GB of graphics memory" },
      { "value": "org/mytts-small", "label": "Small", "note": "faster; 3 GB" }
    ]
  }
}
```

- `name` is the engine name in config, the `--backend` of the server and the folder `sidecar_dir\envs\<name>`.
- `pip` and `module`: what `setup.ps1` installs after PyTorch, and the import that proves it worked.
- `port`: unique, on 127.0.0.1. voice's server answers only there.
- `clones`: the backend takes a reference clip. `transcript`: cloning needs what the clip says. voice then asks for it, and fills it with speech recognition when that is installed.
- `extra`: settings sent with every request (`req["extra"]`).
- `expressive`: what the engine does with voice's sound tags and moods, in the one form every engine uses; leave it out for none. `sounds` maps voice's tag names (`laugh`, `chuckle`, `giggle`, `sigh`, `gasp`, `cough`, `clear_throat`, `sniff`, `sob`, `groan`, `yawn`, `breath`) to the engine's own token (`"laugh": "[laugh]"`); voice puts that token where the tag was and drops tags the engine lacks. `emotion` is `none`, `intensity` or `labels` (with `moods`: a mood to settings sent in `extra`, or to a token put before the text) or `instruct` (with `instruct`, a sentence with `{mood}` in it, sent as `extra.instruct`). The Integrations window and `voice_control list_presets` show it.
- `prompts`: the backend saves a **voice prompt** (see below), so a cloned voice speaks without its recording.
- `prompts_as` (optional): the name of another backend whose voice prompt files this one reads and writes, when it runs the same model another way (`qwen3_fast` reads and writes `qwen3`'s). voice then sends a prompt made by either to both, as long as the model size matches; give both the same `model` option values.
- `batch`: the backend renders several sentences in one call (`synthesize_batch(req, texts)`, `POST /synthesize_batch`). For a model slower than its own speech this matters most: voice gives it up to four sentences at once, from the first one on, so speech starts a moment later and then flows without gaps; the next batch renders while one plays.
- `env` (optional): run in another backend's Python environment when it needs the same packages (Chatterbox Turbo runs in `chatterbox`'s). Setup then installs nothing a working import already gives, records the backend's own stages in that environment, and the Integrations window offers no Remove for it.
- `prefetch` (optional): Hugging Face models setup downloads with the backend, into the sidecar's `models` folder, so the first sentence does not wait for them.
- `option` (optional): one setting the Integrations window offers as a picker. The server gets it as `--option key=value`. A change is saved as `[sidecar.<name>] key = "..."` in `settings.toml`, and voice starts the server again with it on the next request, since `/health` reports the options a server runs with.

`go test ./sidecar` checks the list: required fields, unique ports, an option default that is one of its choices.

## 2. A Python module `sidecar/voice_sidecar/backends/<name>.py`

```python
from . import VoicePrints, device

class Backend:
    clones = True

    def __init__(self, options):          # load the model; options from --option
        ...
        self.prints = VoicePrints("mytts-" + model_id, compute, save=save, load=load)

    def synthesize(self, req):            # -> (samples, sample_rate)
        prompt = self.prints.get(req["ref_audio"], req.get("ref_text") or "")
        ...
```

`req` has `text`, `lang` (en, pl, ...), `ref_audio` (a WAV path), `ref_text`, `speed` and `extra`. Return float samples in -1..1 and their rate.

**VoicePrints** keeps whatever the model computes from a reference clip (an embedding, a prompt), so the clip is analysed once, not for every sentence. It keeps the result in memory and as a file in `sidecar_dir\voiceprints`, keyed by the clip (path, size, time), the transcript and the tag. Put into the tag whatever else the result depends on (the model, a mode). Then a new recording, transcript or model makes a new one by itself.

**Saved voice prompts** (optional, `"prompts": true`). Give the backend `make_prompt(ref_audio, ref_text, out)`: it computes everything cloning needs from the recording once, saves it to `out` together with the model and its revision, and returns `{"model", "revision", ...}`. voice calls it through `POST /prompt` when a voice is cloned, or on `voice clone prompt <name>`. From then on `req["voice_prompt"]` names that file, and the backend reads it instead of `ref_audio`, without opening or even looking for the recording. voice sends the prompt only to the engine and model size that made it; another model size gets the recording, as before. `qwen3.py` is the example: the prompt keeps the speaker embedding, the reference audio's codes, the transcript and the mode flags, not just the embedding.

Load the model in `__init__`: the server listens only after that, so `/health` answering means ready. Keep everything local. Downloading public model weights is fine, and they go to `sidecar_dir\models` (`HF_HOME`). Recordings, transcripts and profiles never leave the machine.

## 3. An integration file `integrations/<name>.toml`

```toml
id = "mytts"
group = "gpu"
order = 50
title = "My TTS"
about = "One sentence: what it gives the user."
size = "5-10 GB"
uses = "sidecar"
backend = "mytts"
requires = ["python"]
```

This adds it to the Integrations window with Install, Reinstall, Remove and the option picker.

## 4. Try it

```powershell
voice sidecar setup mytts                      # or Install in the Integrations window
voice clone add me clip.wav -engine mytts -text "what the clip says" -rights "my own voice"
voice clone preview me
```

The server logs to `sidecar_dir\<name>.log`. `python -m voice_sidecar --backend mytts --port 8769` runs it by hand, from `envs\<name>\Scripts\python.exe` with `PYTHONPATH` set to the `sidecar` folder.

## Without the sidecar

A model that is not Python, or runs elsewhere, needs no entry. An `[engines.x]` table in `config.toml` with `type = "exec"` runs any program that writes a WAV file. With `type = "http"` it talks to any server that speaks the same three routes (`POST /synthesize`, `GET /health`, `GET /voices`). See [config.example.toml](../config.example.toml).
