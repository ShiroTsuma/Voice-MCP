# Higgs Audio v3 with voice

[Higgs Audio v3 TTS 4B](https://huggingface.co/bosonai/higgs-audio-v3-tts-4b) (Boson AI) speaks with emotions, sounds and styles, clones a voice from a recording and its transcript, and covers 100+ languages. It is released for **research and non-commercial use**; read its license before you use it.

It does not run in voice's Python sidecar: Boson serves it through **SGLang-Omni** (or vLLM-Omni), which run on Linux. On Windows that means WSL2 (or Docker Desktop with GPU support). voice does not start that server; it talks to it over the OpenAI speech API (`POST /v1/audio/speech`) with its `openai` engine, and the Integrations window (GPU voices) shows whether it answers.

> These steps follow Boson's model card and the SGLang-Omni docs. Check them there if something has moved.

## 1. The server, in WSL2

In an Ubuntu WSL2 shell with a recent NVIDIA driver on Windows (the GPU shows in `nvidia-smi` inside WSL):

```bash
git clone https://github.com/sgl-project/sglang-omni.git && cd sglang-omni
uv venv .venv -p 3.12 && source .venv/bin/activate
uv pip install -v -e .
hf download bosonai/higgs-audio-v3-tts-4b        # about 10 GB; accept the model's terms on Hugging Face first if it asks
sgl-omni serve --model-path bosonai/higgs-audio-v3-tts-4b --port 8000
```

Tried on Windows 11 with an RTX 5080 (16 GB) in WSL2 Ubuntu 24.04:

- **nvcc 12.9 or newer** must come first on PATH: DeepGEMM compiles kernels at startup and stops with "NVCC version must be at least 12.9" otherwise. Ubuntu's own `/usr/bin/nvcc` is 12.0; NVIDIA's CUDA toolkit for WSL installs 12.9+ under `/usr/local/cuda-12.9`.
- **The first start takes long** (over 10 minutes) while PyTorch tunes kernels for the card, longer than SGLang-Omni's 10-minute startup limit; raise it and keep the tuning cache so later starts take a few minutes.
- **It takes the whole card**: about 15.6 GB in use with the server idle. Stop it before other GPU work (training, voice's own GPU voices).

A start script with all of that, `~/higgs/serve.sh`:

```bash
#!/usr/bin/env bash
export CUDA_HOME=/usr/local/cuda-12.9
export PATH="$CUDA_HOME/bin:$PATH"
export SGLANG_OMNI_STARTUP_TIMEOUT=3600                     # the first start tunes kernels for a long while
export TORCHINDUCTOR_CACHE_DIR="$HOME/.cache/torchinductor" # and keeps that for later starts
cd ~/sglang-omni && source .venv/bin/activate
exec sgl-omni serve --model-path bosonai/higgs-audio-v3-tts-4b --port 8000
```

With the script at `~/higgs/serve.sh` in a WSL distribution called Ubuntu, Integrations (GPU voices, Higgs Audio v3) has **Start server** and **Stop server**: the server takes the whole graphics card while it runs, so stop it before other GPU work. Its output goes to `~/higgs/server.log`. For another setup (distribution, script, Docker, a remote machine), copy `integrations\higgs.toml` next to voice.exe into the `integrations` folder in voice's data folder and change its `start` and `stop` commands. By hand: `wsl -d Ubuntu -- ~/higgs/serve.sh`, and Ctrl+C. The first request after a start takes about 10 s, later ones about 3-4 s to the first sound.

Or with Docker, from the model card: `docker pull lmsysorg/sglang-omni:dev`, run it with `--gpus all --network host`, and start the same `sgl-omni serve` command inside.

WSL2 forwards `localhost`, so Windows reaches the server at `http://127.0.0.1:8000`. The model is about 4B parameters in bf16; give it the graphics card to itself (12-16 GB). Boson states no figure.

### AMD and Intel graphics cards

The documented way to serve Higgs is for **NVIDIA** cards (CUDA). SGLang-Omni supports NVIDIA and Intel Arc (XPU), but **not AMD**, and its Intel support does not list Higgs. vLLM-Omni lists ROCm (AMD) support in general, but its Higgs recipe names CUDA only; Higgs on ROCm is untested by Boson and by voice. With an AMD card:

- run the server on another machine with an NVIDIA card, or a rented GPU server (below), and point voice at it; or
- try vLLM-Omni on native Linux with ROCm (not WSL) at your own risk;
- or use voice's own GPU engines: its sidecar sets Chatterbox, Chatterbox Turbo and XTTS up with AMD's ROCm PyTorch for Windows on recent Radeon cards (the list is in `sidecar/profiles/amd-rocm-windows.json`); Chatterbox shows moods as intensity, and Chatterbox Turbo makes laughs, sighs and coughs. Qwen3-TTS, F5-TTS and OmniVoice need an NVIDIA card (`gpus` in `sidecar/backends.json`), so designing a voice from a description does too.

### Or a server elsewhere

voice only needs the server's address, so it does not have to be this PC: another machine on your network with a GPU (`http://192.168.1.50:8000`), a rented GPU server, or a hosted service that speaks the OpenAI speech API. Set `url` (and `api_key`, sent only to that server, if it wants one) in your `config.toml`. Over the internet, every sentence waits for the network, so speech starts a little later.

**What leaves your PC:** every request carries the text, and for a cloned voice its recording and transcript too. Point a cloned voice only at a server you trust with that recording; a voice you may not share belongs on a server of your own.

## 2. voice

voice ships the engine ready (`[engines.higgs]` in its built-in config):

```toml
[engines.higgs]
type = "openai"
url = "http://127.0.0.1:8000"
model = "bosonai/higgs-audio-v3-tts-4b"
clone = "references"          # a cloned voice's recording and transcript go along as references
params = { temperature = 0.8, top_p = 0.8, top_k = 30 }
```

Change the url in your `config.toml` if the server runs elsewhere. Integrations (GPU voices, Higgs Audio v3) shows whether it answers, and its **Setup guide** opens this page. Then:

- the preset **higgs** speaks with the model's default voice (Kokoro speaks while the server is down);
- a cloned voice can use it: `voice_clone add` (or `design`) with `engine: "higgs"`. The recording goes in each request as a data URL, so the server needs no access to voice's files; give the transcript, it makes the clone much closer.

## 3. Emotions and sounds

voice's tags and moods turn into Higgs' own tokens (`[engines.higgs.expressive]`):

| voice | Higgs |
| --- | --- |
| `{laugh}` | `<\|sfx:laughter\|>Haha` |
| `{chuckle}`, `{giggle}` | `<\|sfx:laughter\|>Hehe` |
| `{sigh}` | `<\|sfx:sigh\|>Ahh` |
| `{cough}`, `{clear_throat}` | `<\|sfx:cough\|>Ahem` |
| `{sniff}` | `<\|sfx:sniff\|>Sff` |
| `{sob}` | `<\|sfx:crying\|>Sob` |
| `{groan}` | `<\|sfx:humming\|>Mmm` |
| mood happy, excited, amused, calm, tender, sad, angry, afraid, surprised | `<\|emotion:elation\|>`, `enthusiasm`, `amusement`, `contentment`, `affection`, `sadness`, `anger`, `fear`, `surprise` |
| mood teasing / sarcastic | amusement with `expressive_high` / bitterness with `expressive_low` |

`{gasp}`, `{yawn}` and `{breath}` have no Higgs token and are left out; `{pause}` is made by voice. Edit the table in your config to map them otherwise; Boson's [prompting guide](https://huggingface.co/bosonai/higgs-tts-3-4b/blob/main/PROMPTING.md) lists every token Higgs knows.

## Other speech servers

The `openai` engine works with any server that speaks the OpenAI speech API and returns WAV: add an `[engines.<name>]` with `type = "openai"`, its `url`, `model` and `voice`, `clone = "none"` if it cannot clone, and an `expressive` table for what it supports.
