"""Chatterbox Multilingual V3 (Resemble AI, MIT): zero-shot cloning in 23
languages including Polish. Resemble retired Chatterbox in August 2026; V3
(June 2026) is the family's last multilingual model, the same size as the
original (0.5B) with better speaker similarity and fewer hallucinations.

Options: model=multilingual|english, t3=v3|v2 (the multilingual checkpoint;
v2 is the original), exaggeration=0.5, cfg_weight=0.5, device=cuda|cpu. Per
request, extra may override exaggeration, cfg_weight and temperature. Low
exaggeration with cfg_weight around 0.3 gives a calm, slow delivery.

An environment set up before V3 has chatterbox-tts 0.1.7 from PyPI, which
knows only the original checkpoint: the backend then speaks with that one and
says so in its log, until Reinstall updates it.

A cloned voice's clip is analysed once (see VoicePrints); later sentences in
that voice start from the stored result.
"""

import sys

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
        tag = "en"
        if self.multilingual:
            from chatterbox.mtl_tts import ChatterboxMultilingualTTS as Model, Conditionals

            t3 = options.get("t3", "v3")
            import inspect

            if "t3_model" in inspect.signature(Model.from_pretrained).parameters:
                self.model = Model.from_pretrained(device=dev, t3_model=t3)
            else:  # chatterbox-tts 0.1.7: the original checkpoint only
                print("chatterbox: this environment predates Multilingual V3; speaking with the original model. "
                      "Update Chatterbox in the Integrations window to get it.", file=sys.stderr, flush=True)
                self.model = Model.from_pretrained(device=dev)
                t3 = "v2"
            print(f"chatterbox: multilingual {t3}", file=sys.stderr, flush=True)
            # A voice print holds the speaker embedding and the reference's
            # speech tokens, which another checkpoint may read differently.
            tag = "mtl" if t3 == "v2" else "mtl-" + t3
        else:
            from chatterbox.tts import ChatterboxTTS as Model, Conditionals

            self.model = Model.from_pretrained(device=dev)
        self.builtin = self.model.conds  # the model's own voice, for requests without a clip

        def compute(path, _text):
            self.model.prepare_conditionals(path, exaggeration=self.defaults["exaggeration"])
            return self.model.conds

        self.prints = VoicePrints(
            "chatterbox-" + tag,
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
