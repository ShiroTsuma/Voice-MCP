"""Chatterbox (Resemble AI, MIT): zero-shot cloning; the multilingual model
speaks 23 languages including Polish.

Options: model=multilingual|english, exaggeration=0.5, cfg_weight=0.5,
device=cuda|cpu. Per request, extra may override exaggeration, cfg_weight
and temperature. Low exaggeration with cfg_weight around 0.3 gives a calm,
slow delivery.
"""

from . import device, to_numpy


class Backend:
    clones = True

    def __init__(self, options):
        self.multilingual = options.get("model", "multilingual") != "english"
        self.defaults = {
            "exaggeration": float(options.get("exaggeration", 0.5)),
            "cfg_weight": float(options.get("cfg_weight", 0.5)),
        }
        dev = device(options)
        if self.multilingual:
            from chatterbox.mtl_tts import ChatterboxMultilingualTTS

            self.model = ChatterboxMultilingualTTS.from_pretrained(device=dev)
        else:
            from chatterbox.tts import ChatterboxTTS

            self.model = ChatterboxTTS.from_pretrained(device=dev)

    def synthesize(self, req):
        params = dict(self.defaults)
        params.update({k: float(v) for k, v in (req.get("extra") or {}).items() if k in ("exaggeration", "cfg_weight", "temperature")})
        if req.get("ref_audio"):
            params["audio_prompt_path"] = req["ref_audio"]
        if self.multilingual:
            params["language_id"] = (req.get("lang") or "en").split("-")[0]
        wav = self.model.generate(req["text"], **params)
        return to_numpy(wav), self.model.sr
