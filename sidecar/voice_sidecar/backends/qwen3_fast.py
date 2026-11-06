"""Qwen3-TTS through faster-qwen3-tts (MIT; the model's own license applies to the weights):
the same model and voices as the qwen3 backend, run with CUDA graphs, many times faster on an
NVIDIA card (on an RTX 5080 a sentence in about 0.9 s instead of about 11 s). It reads and
makes the same voice prompt files as qwen3, so a cloned voice works with either.

It needs CUDA: on another card, or when it fails, the qwen3 backend is the choice.

Options: model=Qwen/Qwen3-TTS-12Hz-1.7B-Base (or -0.6B-Base), dtype=bfloat16|float16.
Per request, extra may set temperature, top_p, top_k and repetition_penalty.
"""

import sys

from . import VoicePrints
from .qwen3 import SAMPLING, Backend as Qwen3

# The model's longest output for one call, in codec steps (12 a second): about three minutes.
MAX_STEPS = 2048


class Backend(Qwen3):
    clones = True

    def __init__(self, options):
        import torch
        from faster_qwen3_tts import FasterQwen3TTS

        if not torch.cuda.is_available():
            raise RuntimeError("qwen3_fast needs an NVIDIA graphics card with CUDA; use the qwen3 backend instead")
        self.model_id = options.get("model", "Qwen/Qwen3-TTS-12Hz-1.7B-Base")
        self.device = "cuda:0"
        default = "bfloat16" if torch.cuda.is_bf16_supported() else "float16"
        dtype = getattr(torch, options.get("dtype", default))
        self.fast = FasterQwen3TTS.from_pretrained(self.model_id, device="cuda", dtype=dtype)
        self.fast.warmup()  # captures the CUDA graphs now, not on the first sentence
        self.model = self.fast.model  # the qwen_tts model: voice prompts are made and read as qwen3 does
        supported = getattr(self.model, "get_supported_languages", lambda: None)()
        self.supported = {str(s).lower() for s in supported} if supported else None
        print(f"qwen3_fast: {self.model_id} on {self.device} ({options.get('dtype', default)}, CUDA graphs); "
              f"languages {sorted(self.supported) if self.supported else 'unknown'}", file=sys.stderr, flush=True)

        def compute(path, text):
            return self.model.create_voice_clone_prompt(ref_audio=path, ref_text=text or None, x_vector_only_mode=not text)

        def save(prompt, file):
            items = [{k: (v.detach().cpu() if hasattr(v, "detach") else v) for k, v in vars(item).items()} for item in prompt]
            torch.save(items, file)

        def load(file):
            from qwen_tts.inference.qwen3_tts_model import VoiceClonePromptItem

            items = torch.load(file, map_location="cpu", weights_only=True)
            return [VoiceClonePromptItem(**{k: (v.to(self.device) if hasattr(v, "to") else v) for k, v in item.items()}) for item in items]

        # The same tag as qwen3: a print one made, the other reads.
        self.prints = VoicePrints("qwen3-" + self.model_id.split("/")[-1], compute, save=save, load=load)
        self.revision = self._revision()
        self.prompts = {}

    # No batches (backends.json has no "batch"): with CUDA graphs one sentence renders faster
    # than it is spoken, so voice asks sentence by sentence and the first is heard sooner.
    synthesize_batch = None

    def synthesize(self, req):
        prompt = self._prompt(req)
        params = {k: v for k, v in (req.get("extra") or {}).items() if k in SAMPLING}
        if "top_k" in params:
            params["top_k"] = int(params["top_k"])
        ref_text = getattr(prompt[0], "ref_text", None) or ""
        audio, rate = self.fast.generate_voice_clone(text=req["text"], language=self.language(req.get("lang")),
                                                     voice_clone_prompt=prompt, ref_text=ref_text,
                                                     max_new_tokens=MAX_STEPS, **params)
        a = audio[0] if isinstance(audio, (list, tuple)) else audio
        return (a.float().cpu().numpy() if hasattr(a, "cpu") else a), rate
