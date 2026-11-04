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

    def language(self, lang):
        name = LANGUAGES.get((lang or "en").split("-")[0].lower())
        if name and (self.supported is None or name.lower() in self.supported):
            return name
        return "Auto"

    def synthesize(self, req):
        if not req.get("ref_audio"):
            raise ValueError("qwen3 clones voices: it needs a cloned voice (ref_audio)")
        text = (req.get("ref_text") or "").strip()
        prompt = self.prints.get(req["ref_audio"], text)
        params = {k: v for k, v in (req.get("extra") or {}).items() if k in SAMPLING}
        if "top_k" in params:
            params["top_k"] = int(params["top_k"])
        wavs, sr = self.model.generate_voice_clone(text=req["text"], language=self.language(req.get("lang")), voice_clone_prompt=prompt, **params)
        return wavs[0], sr
