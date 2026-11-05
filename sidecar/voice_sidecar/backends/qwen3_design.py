"""Qwen3-TTS voice design (Qwen team; see the model card for its license): a
new voice from a description in words ("a raspy older woman, slow and dry"),
through the official qwen-tts package. It runs in the Qwen3-TTS environment.

voice uses it once per designed voice: it renders a sample sentence, and that
sample becomes an ordinary cloned voice, so the voice stays the same in every
line (the design model invents it anew on each call).

Per request, extra must set instruct (the description); temperature, top_p,
top_k and repetition_penalty may be set too. Options: model (default
Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign), attention, dtype, device as for qwen3.
"""

import sys

from . import device
from .qwen3 import LANGUAGES, SAMPLING


class Backend:
    clones = False

    def __init__(self, options):
        import torch
        from qwen_tts import Qwen3TTSModel

        self.model_id = options.get("model", "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign")
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
            print(f"qwen3_design: attention {attention} unavailable ({e}); using eager", file=sys.stderr, flush=True)
            self.model = Qwen3TTSModel.from_pretrained(self.model_id, device_map=self.device, dtype=dtype, attn_implementation="eager")
        print(f"qwen3_design: {self.model_id} on {self.device}", file=sys.stderr, flush=True)

    def synthesize(self, req):
        extra = req.get("extra") or {}
        instruct = str(extra.get("instruct") or "").strip()
        if not instruct:
            raise ValueError("voice design needs a description of the voice (extra.instruct)")
        params = {k: v for k, v in extra.items() if k in SAMPLING}
        if "top_k" in params:
            params["top_k"] = int(params["top_k"])
        lang = LANGUAGES.get((req.get("lang") or "en").split("-")[0].lower(), "Auto")
        wavs, sr = self.model.generate_voice_design(text=req["text"], instruct=instruct, language=lang, **params)
        return wavs[0], sr