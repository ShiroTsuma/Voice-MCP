"""Speech sidecar for voice: runs one Python TTS backend behind a small HTTP API.

    python -m voice_sidecar --backend chatterbox --port 8765

POST /synthesize  {"text", "lang", "voice", "ref_audio", "ref_text", "speed", "extra"} -> audio/wav;
                  a backend with "flow" in backends.json also takes "flow" (clip or
                  previous), "prev_audio" (base64 WAV) and "prev_text": the sentence
                  just said in this voice, to speak this one from
GET  /health      {"backend", "ready", "clones", "options"}
GET  /voices      [{"name", "lang", "gender"}]
POST /shutdown    exits, so voice can start it with other options (a model size)
POST /idle        {"seconds"}: exit after this long without synthesis (0: stay)
POST /touch       counts as a request, so the idle time starts again (a session
                  asked voice to keep its voice loaded); synthesizes nothing
POST /synthesize_batch  as /synthesize with "texts" (a list) instead of "text" ->
                  {"audio": [base64 WAV, ...]}: one voice, several sentences
                  generated together (backends with synthesize_batch: qwen3)
POST /prompt      {"ref_audio", "ref_text", "out"} -> {"model", "revision", ...}: a
                  backend that keeps voice prompts (qwen3) makes one from the clip
                  and saves it to out; /synthesize then takes "voice_prompt" (that
                  file) instead of ref_audio and reads no recording.

The built-in backends are listed in ../backends.json, which voice and
setup.ps1 read too.

Requests are served one at a time: a GPU model is not re-entrant, and voice
queues utterances anyway.
"""

import argparse
import array
import base64
import hashlib
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


# The exit code of a server that could not load its models offline: voice
# starts it once more with HF_HUB_OFFLINE=0, so missing files download.
EXIT_RETRY_ONLINE = 75


def hub_cache():
    if os.environ.get("HF_HUB_CACHE"):
        return os.environ["HF_HUB_CACHE"]
    home = os.environ.get("HF_HOME") or os.path.join(os.path.expanduser("~"), ".cache", "huggingface")
    return os.path.join(home, "hub")


def cached(repo):
    """Whether the Hugging Face cache holds a downloaded snapshot of repo."""
    snapshots = os.path.join(hub_cache(), "models--" + repo.replace("/", "--"), "snapshots")
    try:
        return any(os.listdir(os.path.join(snapshots, d)) for d in os.listdir(snapshots))
    except OSError:
        return False


def loaded_marker(name, options):
    """A file saying this backend with these options loaded completely, so all
    it needs from the hub is in the cache."""
    key = hashlib.sha1(json.dumps([name, sorted(options.items())]).encode()).hexdigest()[:16]
    return os.path.join(hub_cache(), ".voice-loaded", f"{name}-{key}")


def hub_models(name, options):
    """The hub models a built-in backend loads: its model option, or what
    backends.json has setup download for it."""
    if options.get("model"):
        return [options["model"]]
    path = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "backends.json")
    try:
        with open(path, encoding="utf-8") as f:
            for b in json.load(f)["backends"]:
                if b.get("name") == name:
                    return list(b.get("prefetch") or [])
    except (OSError, ValueError, KeyError):
        pass
    return []


def choose_offline(name, options):
    """Load from the cache without asking the hub when everything is there: a
    hub that cannot be reached (a VPN, a dropped connection) would otherwise
    stop a model that needs nothing from it. Online only when HF_HUB_OFFLINE
    is set (voice sets 0 to retry online) or nothing says the files are here."""
    if "HF_HUB_OFFLINE" in os.environ:
        return False
    models = hub_models(name, options)
    if not os.path.exists(loaded_marker(name, options)) and not (models and all(cached(m) for m in models)):
        return False
    os.environ["HF_HUB_OFFLINE"] = "1"
    os.environ["TRANSFORMERS_OFFLINE"] = "1"
    return True


def load_backend(name, options):
    module = importlib.import_module(f"voice_sidecar.backends.{name}" if name in BUILTIN else name)
    return module.Backend(options)


def gpu_mb():
    """The graphics memory this server has taken at most, in MB: what PyTorch
    reserved at its peak plus the CUDA context (about 400 MB, which PyTorch
    does not count). 0 when it runs without a GPU or before it used one."""
    torch = sys.modules.get("torch")
    try:
        if torch is None or not torch.cuda.is_available() or not torch.cuda.is_initialized():
            return 0
        return int(torch.cuda.max_memory_reserved() / 2**20) + 400
    except Exception:
        return 0


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
    idle = 0.0  # seconds without synthesis before the server exits; 0 stays
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
            self.reply(200, {"backend": self.name, "ready": True, "clones": bool(getattr(self.backend, "clones", False)), "options": self.options, "idle": Handler.idle, "gpu_mb": gpu_mb()})
        elif self.path == "/voices":
            self.reply(200, list(getattr(self.backend, "voices", lambda: [])()))
        else:
            self.reply(404, {"error": "not found"})

    def do_POST(self):
        Handler.last_request = time.monotonic()
        if self.path == "/idle":
            try:
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
                Handler.idle = max(0.0, float(body.get("seconds", 0)))
            except (ValueError, TypeError) as e:
                self.reply(400, {"error": f"bad idle: {e}"})
                return
            print(f"voice_sidecar idle time now {Handler.idle:.0f} s", file=sys.stderr, flush=True)
            self.reply(200, {"idle": Handler.idle})
            return
        if self.path == "/touch":  # last_request was set above
            self.reply(200, {"idle": Handler.idle})
            return
        if self.path == "/shutdown":
            self.reply(200, {"ok": True})
            print("voice_sidecar asked to exit", file=sys.stderr, flush=True)
            threading.Thread(target=lambda: (time.sleep(0.2), os._exit(0)), daemon=True).start()
            return
        if self.path not in ("/synthesize", "/synthesize_batch", "/prompt"):
            self.reply(404, {"error": "not found"})
            return
        try:
            req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        except ValueError as e:
            self.reply(400, {"error": f"bad JSON: {e}"})
            return
        if self.path == "/synthesize_batch":
            batch = getattr(self.backend, "synthesize_batch", None)
            texts = [str(t) for t in (req.get("texts") or []) if str(t).strip()]
            if batch is None or not texts:
                self.reply(400, {"error": f"backend {self.name} renders no batches" if batch is None else "texts is empty"})
                return
            try:
                with self.lock:
                    samples, rate = batch(req, texts)
                Handler.last_request = time.monotonic()
                self.reply(200, {"audio": [base64.b64encode(to_wav(s, rate)).decode() for s in samples]})
            except Exception as e:
                traceback.print_exc(file=sys.stderr)
                self.reply(500, {"error": f"{type(e).__name__}: {e}"})
            return
        if self.path == "/prompt":
            make = getattr(self.backend, "make_prompt", None)
            if make is None:
                self.reply(400, {"error": f"backend {self.name} keeps no voice prompts"})
                return
            try:
                with self.lock:
                    meta = make(req.get("ref_audio") or "", req.get("ref_text") or "", req.get("out") or "")
                self.reply(200, meta)
            except Exception as e:
                traceback.print_exc(file=sys.stderr)
                self.reply(500, {"error": f"{type(e).__name__}: {e}"})
            return
        if not str(req.get("text", "")).strip():
            self.reply(400, {"error": "text is empty"})
            return
        if req.get("voice_prompt") and not hasattr(self.backend, "make_prompt"):
            self.reply(400, {"error": f"backend {self.name} cannot use a voice prompt"})
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
    offline = choose_offline(args.backend, options)
    if offline:
        print(f"voice_sidecar {args.backend}: models are in the cache, loading them offline", file=sys.stderr, flush=True)
    try:
        Handler.backend = load_backend(args.backend, options)  # load before listening: /health means ready
    except Exception:
        if not offline:
            raise
        traceback.print_exc(file=sys.stderr)
        try:
            os.remove(loaded_marker(args.backend, options))
        except OSError:
            pass
        print(f"voice_sidecar {args.backend}: could not load offline; voice starts it again online", file=sys.stderr, flush=True)
        sys.stderr.flush()
        os._exit(EXIT_RETRY_ONLINE)
    try:
        marker = loaded_marker(args.backend, options)
        os.makedirs(os.path.dirname(marker), exist_ok=True)
        with open(marker, "w", encoding="utf-8") as f:
            f.write(time.strftime("%Y-%m-%d %H:%M:%S") + "\n")
    except OSError:
        pass
    server = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"voice_sidecar {args.backend} ready on http://{args.host}:{args.port}", file=sys.stderr, flush=True)
    # Exit after idle seconds without synthesis, freeing GPU memory (0: stay);
    # voice starts the server again on the next request that needs it, and
    # changes the time while it runs (POST /idle).
    Handler.idle = args.idle_seconds

    def watch():
        while True:
            time.sleep(5)
            idle = Handler.idle
            if idle > 0 and not Handler.lock.locked() and time.monotonic() - Handler.last_request > idle:
                print("voice_sidecar idle, exiting", file=sys.stderr, flush=True)
                os._exit(0)

    Handler.last_request = time.monotonic()
    threading.Thread(target=watch, daemon=True).start()
    server.serve_forever()


if __name__ == "__main__":
    main()
