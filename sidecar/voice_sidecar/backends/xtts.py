"""XTTS-v2 (Coqui, CPML non-commercial) via the maintained coqui-tts
package: cloning in 17 languages including Polish.

Options: model=tts_models/multilingual/multi-dataset/xtts_v2, device=cuda|cpu.
"""

from . import device


class Backend:
    clones = True

    def __init__(self, options):
        from TTS.api import TTS

        self.tts = TTS(options.get("model", "tts_models/multilingual/multi-dataset/xtts_v2")).to(device(options))
        self.rate = self.tts.synthesizer.output_sample_rate

    def synthesize(self, req):
        if not req.get("ref_audio"):
            raise ValueError("xtts needs a cloned voice (ref_audio)")
        wav = self.tts.tts(
            text=req["text"],
            speaker_wav=req["ref_audio"],
            language=(req.get("lang") or "en").split("-")[0],
            speed=float(req.get("speed") or 1.0),
        )
        return wav, self.rate
