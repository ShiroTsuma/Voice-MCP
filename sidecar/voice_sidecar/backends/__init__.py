"""Backends implement:

    class Backend:
        clones: bool                      # accepts ref_audio
        def __init__(self, options: dict) # load the model; options come from --option
        def synthesize(self, req: dict) -> (samples, sample_rate)
        def voices(self) -> list[dict]    # optional

req has text, lang, voice, ref_audio (path), ref_text, speed and extra (dict).
"""

import hashlib
import os
import sys
import time


def device(options):
    if options.get("device"):
        return options["device"]
    import torch

    return "cuda" if torch.cuda.is_available() else "cpu"


def to_numpy(x):
    if hasattr(x, "detach"):
        x = x.detach().float().cpu().numpy()
    return x


class VoicePrints:
    """Keeps what a backend computes from a reference clip (its voice print),
    so a clip is analysed once instead of for every sentence: in memory while
    the server runs, and as a file under voiceprints\\ in the server's folder
    for the servers after it. A clip recorded again has another size or time
    and gets a new print.

    compute(path) makes a print; save(print, file) and load(file) store it.
    """

    def __init__(self, tag, compute, save=None, load=None):
        self.tag, self.compute, self.save, self.load = tag, compute, save, load
        self.memory = {}

    def file(self, path):
        st = os.stat(path)
        raw = f"{self.tag}|{os.path.abspath(path).lower()}|{st.st_size}|{st.st_mtime_ns}"
        key = hashlib.sha1(raw.encode()).hexdigest()[:20]
        return os.path.join(os.getcwd(), "voiceprints", f"{self.tag}-{key}.pt")

    def get(self, path):
        file = self.file(path)
        if file in self.memory:
            return self.memory[file]
        start = time.monotonic()
        made, how = None, "computed"
        if self.load and os.path.exists(file):
            try:
                made, how = self.load(file), "loaded"
            except Exception as e:  # a damaged file: compute it again
                print(f"voiceprint {file} unreadable ({e}); computing it again", file=sys.stderr, flush=True)
        if made is None:
            made = self.compute(path)
            if self.save:
                try:
                    os.makedirs(os.path.dirname(file), exist_ok=True)
                    self.save(made, file + ".tmp")
                    os.replace(file + ".tmp", file)
                except Exception as e:  # the print still works from memory
                    print(f"voiceprint not saved: {e}", file=sys.stderr, flush=True)
        name = os.path.basename(os.path.dirname(path)) or path
        print(f"voiceprint for {name} {how} in {time.monotonic() - start:.2f} s", file=sys.stderr, flush=True)
        self.memory[file] = made
        return made
