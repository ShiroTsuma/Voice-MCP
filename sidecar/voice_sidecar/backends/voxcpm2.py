"""VoxCPM2 (OpenBMB; Apache-2.0, code and weights): 2B parameters, 30
languages with Polish among them, 48 kHz. It clones from a clip, closest when
the clip's transcript comes with it.

Options: model=openbmb/VoxCPM2, device=cuda|cpu, steps=10 (diffusion steps
per frame), cfg=2.0. Per request, extra may override steps and cfg.

A voice with a transcript also continues from the clip and what it says,
closer to the recording; one without clones from the clip's sound alone,
which was heard as more natural. The voice decides: voice writes no
transcript for this engine by itself.

No moods: the model takes a style in words in parentheses before the text
("(teasing tone)Hello"), but on a cloned voice that pulled the pitch far off
from take to take (60 Hz on a 146 Hz voice), and the user heard nearly every
such take as wrong. So voice sends none.

The language comes from the text itself; the model takes no language tag.

Speed comes from torch.compile, which needs Triton (triton-windows, installed
with it): about 3x real time on an RTX 5080 instead of 0.45x. The first start
compiles for about three minutes; later ones take about half a minute from
the compile cache, kept in the server's folder (compile-cache) rather than in
%TEMP%, where a cleanup would cost the next start those minutes again.
Without Triton it still speaks, slowly.
"""

import os
import sys

from . import device


class Backend:
    clones = True

    def __init__(self, options):
        os.environ.setdefault("TORCHINDUCTOR_CACHE_DIR", os.path.join(os.getcwd(), "compile-cache"))
        from voxcpm import VoxCPM

        dev = device(options)
        self.model_id = options.get("model", "openbmb/VoxCPM2")
        self.steps = int(options.get("steps", 10))
        self.cfg = float(options.get("cfg", 2.0))
        kw = {"load_denoiser": False}
        try:
            self.model = VoxCPM.from_pretrained(self.model_id, device=dev, **kw)
        except TypeError:  # a release without the device argument picks the card itself
            self.model = VoxCPM.from_pretrained(self.model_id, **kw)
        self.rate = int(getattr(self.model.tts_model, "sample_rate", 48000))
        print(f"voxcpm2: {self.model_id} on {dev}, {self.rate} Hz", file=sys.stderr, flush=True)

    def synthesize(self, req):
        extra = req.get("extra") or {}
        kw = {
            "text": req["text"].strip(),
            "cfg_value": float(extra.get("cfg", self.cfg)),
            "inference_timesteps": int(extra.get("steps", self.steps)),
        }
        if req.get("ref_audio"):
            kw["reference_wav_path"] = req["ref_audio"]
            if req.get("ref_text"):
                # The clip and what it says, as the start the model continues
                # from: closest to the voice.
                kw["prompt_wav_path"] = req["ref_audio"]
                kw["prompt_text"] = req["ref_text"]
        wav = self.model.generate(**kw)
        return wav, self.rate
