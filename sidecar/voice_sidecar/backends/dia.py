"""Dia (Nari Labs, Apache-2.0): English dialogue with non-verbal sounds in the
text ((laughs), (sighs), (gasps), (coughs), (clears throat), ...).

It clones from a 5-10 second clip and its transcript: the transcript goes
before the new text, both as speaker [S1], and the clip is the audio prompt;
only the new text's audio comes back. Without a clip every call may get
another voice, so voice uses it for cloned voices.

Options: model=nari-labs/Dia-1.6B-0626, device=cuda|cpu, dtype=float16.
Per request, extra may override cfg_scale, temperature and top_p.
"""

from . import device

RATE = 44100
TUNABLE = ("cfg_scale", "temperature", "top_p")


def speaker(text):
    text = " ".join(text.split())
    return text if text.startswith("[S1]") else "[S1] " + text


class Backend:
    clones = True

    def __init__(self, options):
        from dia.model import Dia

        dev = device(options)
        kw = {"compute_dtype": options.get("dtype", "float16" if dev == "cuda" else "float32")}
        try:
            import torch

            kw["device"] = torch.device(dev)
        except ImportError:
            pass
        self.model = Dia.from_pretrained(options.get("model", "nari-labs/Dia-1.6B-0626"), **kw)

    def synthesize(self, req):
        # torch.compile needs Triton, which Windows lacks; the rest are Dia's defaults.
        params = {"use_torch_compile": False, "verbose": False}
        params.update({k: float(v) for k, v in (req.get("extra") or {}).items() if k in TUNABLE})
        text = speaker(req["text"])
        if req.get("ref_audio") and req.get("ref_text"):
            text = speaker(req["ref_text"]) + " " + text
            params["audio_prompt"] = req["ref_audio"]
        out = self.model.generate(text, **params)
        if isinstance(out, list):
            out = out[0]
        return out, RATE
