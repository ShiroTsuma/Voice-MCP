"""Backends implement:

    class Backend:
        clones: bool                      # accepts ref_audio
        def __init__(self, options: dict) # load the model; options come from --option
        def synthesize(self, req: dict) -> (samples, sample_rate)
        def voices(self) -> list[dict]    # optional

req has text, lang, voice, ref_audio (path), ref_text, speed and extra (dict).
"""


def device(options):
    if options.get("device"):
        return options["device"]
    import torch

    return "cuda" if torch.cuda.is_available() else "cpu"


def to_numpy(x):
    if hasattr(x, "detach"):
        x = x.detach().float().cpu().numpy()
    return x
