"""OmniVoice (k2-fsa; code Apache-2.0, weights CC-BY-NC): zero-shot cloning
in 600+ languages, Polish included, from a clip and its transcript, with
non-verbal tags in the text ([laughter], [sigh]). About 0.6B parameters.

Options: model=k2-fsa/OmniVoice, device=cuda|cpu, steps=32 (diffusion steps;
16 is faster). Per request, extra may override steps.

Without a transcript OmniVoice would transcribe the clip with Whisper (another
download), so voice sends the clone's transcript.
"""

from . import device

RATE = 24000


class Backend:
    clones = True

    def __init__(self, options):
        import torch
        from omnivoice import OmniVoice

        dev = device(options)
        self.steps = int(options.get("steps", 32))
        self.model = OmniVoice.from_pretrained(
            options.get("model", "k2-fsa/OmniVoice"),
            device_map="cuda:0" if dev == "cuda" else dev,
            dtype=torch.float16 if dev == "cuda" else torch.float32,
        )

    def synthesize(self, req):
        kw = {"text": req["text"], "num_step": int((req.get("extra") or {}).get("steps", self.steps))}
        if req.get("lang"):
            kw["language"] = req["lang"].split("-")[0]
        if req.get("ref_audio"):
            kw["ref_audio"] = req["ref_audio"]
            if req.get("ref_text"):
                kw["ref_text"] = req["ref_text"]
        speed = float(req.get("speed") or 1.0)
        if abs(speed - 1.0) > 0.01:
            kw["speed"] = speed
        audio = self.model.generate(**kw)
        return audio[0], getattr(self.model, "sampling_rate", None) or RATE
