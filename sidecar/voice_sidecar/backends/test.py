"""A dependency-free backend for testing the protocol: a tone per word."""

import math


class Backend:
    clones = True

    def __init__(self, options):
        self.rate = int(options.get("rate", 16000))

    def synthesize(self, req):
        words = max(1, len(str(req["text"]).split()))
        speed = float(req.get("speed") or 1.0)
        n = int(self.rate * 0.15 * words / speed)
        freq = 330.0 if not req.get("ref_audio") else 440.0
        return [0.2 * math.sin(2 * math.pi * freq * i / self.rate) for i in range(n)], self.rate

    def voices(self):
        return [{"name": "tone", "lang": "en", "gender": ""}]
