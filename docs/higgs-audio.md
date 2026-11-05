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

Or with Docker, from the model card: `docker pull lmsysorg/sglang-omni:dev`, run it with `--gpus all --network host`, and start the same `sgl-omni serve` command inside.

WSL2 forwards `localhost`, so Windows reaches the server at `http://127.0.0.1:8000`. The model is about 4B parameters in bf16; give it the graphics card to itself (12-16 GB). Boson states no figure.

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

Change the url in your `config.toml` if the server runs elsewhere. Then:

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
