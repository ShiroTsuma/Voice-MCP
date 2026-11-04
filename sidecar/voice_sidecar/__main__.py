"""Speech sidecar for voice: runs one Python TTS backend behind a small HTTP API.

    python -m voice_sidecar --backend chatterbox --port 8765

POST /synthesize  {"text", "lang", "voice", "ref_audio", "ref_text", "speed", "extra"} -> audio/wav
GET  /health      {"backend", "ready", "clones", "options"}
GET  /voices      [{"name", "lang", "gender"}]
POST /shutdown    exits, so voice can start it with other options (a model size)

The built-in backends are listed in ../backends.json, which voice and
setup.ps1 read too.

Requests are served one at a time: a GPU model is not re-entrant, and voice
queues utterances anyway.
"""

import argparse
import array
import importlib
import io
import json
import os
import sys
import threading
import time
import traceback
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

def builtin():
    path = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "backends.json")
    try:
        with open(path, encoding="utf-8") as f:
            return tuple(b["name"] for b in json.load(f)["backends"])
    except (OSError, ValueError, KeyError):
        return ("chatterbox", "f5", "xtts", "test")


BUILTIN = builtin()


def load_backend(name, options):
    module = importlib.import_module(f"voice_sidecar.backends.{name}" if name in BUILTIN else name)
    return module.Backend(options)


def to_wav(samples, rate):
    try:
        import numpy as np

        pcm = np.clip(np.asarray(samples, dtype=np.float32).reshape(-1), -1.0, 1.0)
        frames = (pcm * 32767).astype("<i2").tobytes()
    except ImportError:  # the test backend runs without numpy
        frames = array.array("h", (int(max(-1.0, min(1.0, s)) * 32767) for s in samples))
        if sys.byteorder != "little":
            frames.byteswap()
        frames = frames.tobytes()
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(int(rate))
        w.writeframes(frames)
    return buf.getvalue()


class Handler(BaseHTTPRequestHandler):
    backend = None
    name = ""
    options = {}
    lock = threading.Lock()
    last_request = time.monotonic()

    def log_message(self, fmt, *args):  # keep stdout quiet; errors go to stderr
        pass

    def reply(self, code, body, ctype="application/json"):
        data = body if isinstance(body, bytes) else json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/health":
            self.reply(200, {"backend": self.name, "ready": True, "clones": bool(getattr(self.backend, "clones", False)), "options": self.options})
        elif self.path == "/voices":
            self.reply(200, list(getattr(self.backend, "voices", lambda: [])()))
        else:
            self.reply(404, {"error": "not found"})

    def do_POST(self):
        Handler.last_request = time.monotonic()
        if self.path == "/shutdown":
            self.reply(200, {"ok": True})
            print("voice_sidecar asked to exit", file=sys.stderr, flush=True)
            threading.Thread(target=lambda: (time.sleep(0.2), os._exit(0)), daemon=True).start()
            return
        if self.path != "/synthesize":
            self.reply(404, {"error": "not found"})
            return
        try:
            req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        except ValueError as e:
            self.reply(400, {"error": f"bad JSON: {e}"})
            return
        if not str(req.get("text", "")).strip():
            self.reply(400, {"error": "text is empty"})
            return
        if req.get("ref_audio") and not getattr(self.backend, "clones", False):
            self.reply(400, {"error": f"backend {self.name} cannot clone voices"})
            return
        try:
            with self.lock:
                samples, rate = self.backend.synthesize(req)
            Handler.last_request = time.monotonic()
            self.reply(200, to_wav(samples, rate), "audio/wav")
        except Exception as e:  # report backend failures to the engine
            traceback.print_exc(file=sys.stderr)
            self.reply(500, {"error": f"{type(e).__name__}: {e}"})


def main():
    ap = argparse.ArgumentParser(prog="voice_sidecar")
    ap.add_argument("--backend", required=True, help=f"one of {', '.join(BUILTIN)} or an importable module with a Backend class")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8765)
    ap.add_argument("--option", action="append", default=[], metavar="KEY=VALUE", help="backend option, repeatable")
    ap.add_argument("--idle-seconds", type=float, default=0, help="exit after this long without synthesis, freeing GPU memory; 0 never")
    args = ap.parse_args()
    options = dict(o.split("=", 1) for o in args.option)
    # Keep downloaded model weights next to the sidecar, not in the user profile.
    os.environ.setdefault("HF_HOME", os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "models"))

    Handler.name = args.backend
    Handler.options = options
    Handler.backend = load_backend(args.backend, options)  # load before listening: /health means ready
    server = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"voice_sidecar {args.backend} ready on http://{args.host}:{args.port}", file=sys.stderr, flush=True)
    if args.idle_seconds > 0:
        # voice starts the server again on the next request that needs it.
        def watch():
            while True:
                time.sleep(5)
                if not Handler.lock.locked() and time.monotonic() - Handler.last_request > args.idle_seconds:
                    print("voice_sidecar idle, exiting", file=sys.stderr, flush=True)
                    os._exit(0)

        Handler.last_request = time.monotonic()
        threading.Thread(target=watch, daemon=True).start()
    server.serve_forever()


if __name__ == "__main__":
    main()
