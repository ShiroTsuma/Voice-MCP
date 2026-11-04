"""Qwen3-TTS (Qwen team; see the model card for its license) through the official qwen-tts package:
cloning from the reference clip and its transcript (in-context, not only a
speaker embedding), in ten languages: English, Chinese, Japanese, Korean,
German, French, Russian, Portuguese, Spanish, Italian. Polish is not among
them; it is spoken with the model's automatic language choice, poorly.

Options: model=Qwen/Qwen3-TTS-12Hz-1.7B-Base (or -0.6B-Base),
attention=sdpa|eager|flash_attention_2, dtype=bfloat16|float16|float32,
device=cuda|cpu. FlashAttention is optional: SDPA, built into PyTorch,
works on Windows. Per request, extra may set temperature, top_p, top_k and
repetition_penalty.

Without a transcript the clone uses the speaker embedding alone (the
package's x_vector_only_mode), which sounds less like the speaker; voice
passes the transcript it has. The prepared voice prompt is kept (see
VoicePrints): a new recording, transcript, model or mode makes a new one.
"""

import sys

from . import VoicePrints, device

# voice's language codes to the names Qwen3-TTS takes.
LANGUAGES = {
    "en": "English", "zh": "Chinese", "ja": "Japanese", "ko": "Korean", "de": "German",
    "fr": "French", "ru": "Russian", "pt": "Portuguese", "es": "Spanish", "it": "Italian",
}
SAMPLING = ("temperature", "top_p", "top_k", "repetition_penalty")


class Backend:
    clones = True

    def __init__(self, options):
        import torch
        from qwen_tts import Qwen3TTSModel

        self.model_id = options.get("model", "Qwen/Qwen3-TTS-12Hz-1.7B-Base")
        dev = device(options)
        self.device = "cuda:0" if dev == "cuda" else dev
        gpu = self.device.startswith("cuda")
        default = "bfloat16" if gpu and torch.cuda.is_bf16_supported() else ("float16" if gpu else "float32")
        dtype = getattr(torch, options.get("dtype", default))
        attention = options.get("attention", "sdpa")
        try:
            self.model = Qwen3TTSModel.from_pretrained(self.model_id, device_map=self.device, dtype=dtype, attn_implementation=attention)
        except (ValueError, ImportError) as e:
            if attention == "eager":
                raise
            print(f"qwen3: attention {attention} unavailable ({e}); using eager", file=sys.stderr, flush=True)
            self.model = Qwen3TTSModel.from_pretrained(self.model_id, device_map=self.device, dtype=dtype, attn_implementation="eager")
        supported = getattr(self.model, "get_supported_languages", lambda: None)()
        self.supported = {str(s).lower() for s in supported} if supported else None
        print(f"qwen3: {self.model_id} on {self.device} ({options.get('dtype', default)}, {attention}); languages {sorted(self.supported) if self.supported else 'unknown'}", file=sys.stderr, flush=True)

        def compute(path, text):
            return self.model.create_voice_clone_prompt(ref_audio=path, ref_text=text or None, x_vector_only_mode=not text)

        def save(prompt, file):
            # Plain tensors and values, so loading needs no qwen_tts class by path.
            items = [{k: (v.detach().cpu() if hasattr(v, "detach") else v) for k, v in vars(item).items()} for item in prompt]
            torch.save(items, file)

        def load(file):
            from qwen_tts.inference.qwen3_tts_model import VoiceClonePromptItem

            items = torch.load(file, map_location="cpu", weights_only=True)
            return [VoiceClonePromptItem(**{k: (v.to(self.device) if hasattr(v, "to") else v) for k, v in item.items()}) for item in items]

        # The tag holds what the prompt depends on besides the clip and text.
        self.prints = VoicePrints("qwen3-" + self.model_id.split("/")[-1], compute, save=save, load=load)
        self.revision = self._revision()
        self.prompts = {}  # saved prompt files in use: path -> (mtime, prompt)

    def _revision(self):
        """The model's Hugging Face commit, so a saved prompt names exactly
        what made it; "" when it cannot be told (a local folder)."""
        rev = getattr(getattr(self.model, "config", None), "_commit_hash", None)
        if rev:
            return str(rev)
        try:
            import os

            from huggingface_hub import snapshot_download

            return os.path.basename(snapshot_download(self.model_id, local_files_only=True))
        except Exception:
            return ""

    def make_prompt(self, ref_audio, ref_text, out):
        """Makes a voice prompt from a clip and its transcript once and saves
        all of it to out: the speaker embedding, the reference audio's codes,
        the transcript and the mode flags, with the model and its revision.
        Synthesis from it then needs no recording."""
        import os

        import torch

        if not ref_audio or not os.path.exists(ref_audio):
            raise ValueError(f"no recording at {ref_audio!r}")
        if not ref_text.strip():
            raise ValueError("a voice prompt needs the transcript of the recording")
        if not out:
            raise ValueError("no file to save the prompt to")
        prompt = self.model.create_voice_clone_prompt(ref_audio=ref_audio, ref_text=ref_text.strip(), x_vector_only_mode=False)
        meta = {
            "format": 1,
            "engine": "qwen3",
            "model": self.model_id,
            "revision": self.revision,
            "ref_text": ref_text.strip(),
            "x_vector_only_mode": False,
            "icl_mode": bool(getattr(prompt[0], "icl_mode", True)),
        }
        items = [{k: (v.detach().cpu() if hasattr(v, "detach") else v) for k, v in vars(item).items()} for item in prompt]
        os.makedirs(os.path.dirname(os.path.abspath(out)), exist_ok=True)
        torch.save({**meta, "items": items}, out + ".tmp")
        os.replace(out + ".tmp", out)
        self.prompts.pop(os.path.abspath(out), None)
        print(f"qwen3: voice prompt saved to {out}", file=sys.stderr, flush=True)
        return meta

    def load_prompt(self, path):
        """A saved prompt, read once and kept while its file stays the same."""
        import os

        import torch
        from qwen_tts.inference.qwen3_tts_model import VoiceClonePromptItem

        path = os.path.abspath(path)
        mtime = os.stat(path).st_mtime_ns
        held = self.prompts.get(path)
        if held and held[0] == mtime:
            return held[1]
        data = torch.load(path, map_location="cpu", weights_only=True)
        if data.get("engine") != "qwen3" or data.get("format") != 1:
            raise ValueError(f"{path} is not a qwen3 voice prompt")
        if data.get("model") != self.model_id:
            raise ValueError(f"the voice prompt was made with {data.get('model')}, this server runs {self.model_id}; "
                             "pick that model size, or make the prompt again")
        if self.revision and data.get("revision") and data["revision"] != self.revision:
            print(f"qwen3: prompt {path} was made with revision {data['revision']}, the model is {self.revision}", file=sys.stderr, flush=True)
        prompt = [VoiceClonePromptItem(**{k: (v.to(self.device) if hasattr(v, "to") else v) for k, v in item.items()}) for item in data["items"]]
        self.prompts[path] = (mtime, prompt)
        print(f"qwen3: voice prompt {path} loaded", file=sys.stderr, flush=True)
        return prompt

    def language(self, lang):
        name = LANGUAGES.get((lang or "en").split("-")[0].lower())
        if name and (self.supported is None or name.lower() in self.supported):
            return name
        return "Auto"

    def synthesize(self, req):
        if req.get("voice_prompt"):
            # A saved prompt: no recording is read or even looked for.
            prompt = self.load_prompt(req["voice_prompt"])
        elif req.get("ref_audio"):
            prompt = self.prints.get(req["ref_audio"], (req.get("ref_text") or "").strip())
        else:
            raise ValueError("qwen3 clones voices: it needs a cloned voice (ref_audio or voice_prompt)")
        params = {k: v for k, v in (req.get("extra") or {}).items() if k in SAMPLING}
        if "top_k" in params:
            params["top_k"] = int(params["top_k"])
        wavs, sr = self.model.generate_voice_clone(text=req["text"], language=self.language(req.get("lang")), voice_clone_prompt=prompt, **params)
        return wavs[0], sr
