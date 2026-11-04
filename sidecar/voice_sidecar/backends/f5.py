"""F5-TTS (code MIT, weights CC-BY-NC): flow-matching cloning; needs the
reference clip's transcript (voice passes it as ref_text).

Options: model=F5TTS_v1_Base, nfe_step=32, device=cuda|cpu.
"""

from . import device


class Backend:
    clones = True

    def __init__(self, options):
        from f5_tts.api import F5TTS

        self.nfe = int(options.get("nfe_step", 32))
        self.model = F5TTS(model=options.get("model", "F5TTS_v1_Base"), device=device(options))

    def synthesize(self, req):
        if not req.get("ref_audio"):
            raise ValueError("f5 needs a cloned voice (ref_audio)")
        if not req.get("ref_text"):
            raise ValueError("f5 needs the transcript of the reference clip")
        wav, sr, _ = self.model.infer(
            ref_file=req["ref_audio"],
            ref_text=req["ref_text"],
            gen_text=req["text"],
            speed=float(req.get("speed") or 1.0),
            nfe_step=self.nfe,
            remove_silence=False,
        )
        return wav, sr
