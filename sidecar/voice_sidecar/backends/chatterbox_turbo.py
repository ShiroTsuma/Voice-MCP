"""Chatterbox Turbo (Resemble AI, MIT): English, with paralinguistic tags in
the text ([laugh], [sigh], [cough], [chuckle], ...) and a few emotion and
style tags ([happy], [angry], [sarcastic], [whispering], ...). It clones from
a clip longer than 5 seconds. Runs in the Chatterbox environment (the same
chatterbox-tts package).

Options: device=cuda|cpu. Per request, extra may override temperature,
top_p, top_k and repetition_penalty.

A cloned voice's clip is analysed once (see VoicePrints).
"""

import re

from . import VoicePrints, device, to_numpy

TUNABLE = ("temperature", "top_p", "top_k", "repetition_penalty")

# voice puts a comma after each sound ("[laugh], okay"), which gives it room
# before the next words; where a sentence ends right after it, the sentence's
# own mark is enough.
_STRAY = re.compile(r",\s*([.!?,;:])")


def tidy(text):
    return _STRAY.sub(r"\1", text).strip().rstrip(",")


class Backend:
    clones = True

    def __init__(self, options):
        from chatterbox.tts_turbo import ChatterboxTurboTTS, Conditionals

        dev = device(options)
        self.model = ChatterboxTurboTTS.from_pretrained(device=dev)
        self.builtin = self.model.conds  # the model's own voice, for requests without a clip

        def compute(path, _text):
            self.model.prepare_conditionals(path)
            return self.model.conds

        self.prints = VoicePrints(
            "chatterbox-turbo",
            compute,
            save=lambda conds, file: conds.save(file),
            load=lambda file: Conditionals.load(file, map_location=dev).to(dev),
        )

    def synthesize(self, req):
        params = {}
        for k, v in (req.get("extra") or {}).items():
            if k in TUNABLE:
                params[k] = int(v) if k == "top_k" else float(v)
        self.model.conds = self.prints.get(req["ref_audio"]) if req.get("ref_audio") else self.builtin
        if self.model.conds is None:
            raise ValueError("Chatterbox Turbo has no voice of its own here; give it a clip to clone")
        wav = self.model.generate(tidy(req["text"]), **params)
        return to_numpy(wav), self.model.sr
