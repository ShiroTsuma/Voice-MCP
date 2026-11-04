"""Chatterbox (Resemble AI, MIT): zero-shot cloning; the multilingual model
speaks 23 languages including Polish.

Options: model=multilingual|english, exaggeration=0.5, cfg_weight=0.5,
device=cuda|cpu. Per request, extra may override exaggeration, cfg_weight
and temperature. Low exaggeration with cfg_weight around 0.3 gives a calm,
slow delivery.

A cloned voice's clip is analysed once (see VoicePrints); later sentences in
that voice start from the stored result.
"""

from . import VoicePrints, device, to_numpy


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
            from chatterbox.mtl_tts import ChatterboxMultilingualTTS as Model, Conditionals
        else:
            from chatterbox.tts import ChatterboxTTS as Model, Conditionals
        self.model = Model.from_pretrained(device=dev)
        self.builtin = self.model.conds  # the model's own voice, for requests without a clip

        def compute(path, _text):
            self.model.prepare_conditionals(path, exaggeration=self.defaults["exaggeration"])
            return self.model.conds

        self.prints = VoicePrints(
            "chatterbox-" + ("mtl" if self.multilingual else "en"),
            compute,
            save=lambda conds, file: conds.save(file),
            load=lambda file: Conditionals.load(file, map_location=dev).to(dev),
        )

    def synthesize(self, req):
        params = dict(self.defaults)
        params.update({k: float(v) for k, v in (req.get("extra") or {}).items() if k in ("exaggeration", "cfg_weight", "temperature")})
        # generate() adjusts the print's exaggeration itself when it differs.
        self.model.conds = self.prints.get(req["ref_audio"]) if req.get("ref_audio") else self.builtin
        if self.multilingual:
            params["language_id"] = (req.get("lang") or "en").split("-")[0]
        wav = self.model.generate(req["text"], **params)
        return to_numpy(wav), self.model.sr
