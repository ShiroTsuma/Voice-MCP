"""A minimal agent on voice's session API, with any OpenAI-compatible model.

It registers a session, polls for what the user dictates to it, answers
with the model, says the answer aloud and reports the end of its turn.
A reply that starts with "@Name:" goes to the session called Name instead,
so agents can talk to each other.

    python examples/agent.py --name Mira --model qwen2.5:0.5b
    python examples/agent.py --name Mira --echo          # no model: repeat

The model endpoint defaults to Ollama's (http://localhost:11434/v1). Only the
Python standard library is used. See docs/http-api.md.
"""

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
import uuid


def discovery_path():
    base = os.environ.get("APPDATA") or os.path.join(
        os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config"))
    return os.path.join(base, "voice", "daemon.json")


class Voice:
    """voice's session API; finds the running background process again
    after it restarts (new port and token)."""

    def __init__(self):
        self.info = None

    def call(self, action, **body):
        for attempt in range(2):
            if self.info is None:
                with open(discovery_path(), encoding="utf-8") as f:
                    self.info = json.load(f)
            req = urllib.request.Request(
                f"http://{self.info['addr']}/v1/session/{action}",
                data=json.dumps(body).encode(),
                headers={"Content-Type": "application/json", "X-Voice-Token": self.info["token"]},
            )
            try:
                with urllib.request.urlopen(req, timeout=30) as r:
                    return json.load(r)["result"]
            except urllib.error.HTTPError as e:
                if e.code == 403 and attempt == 0:
                    self.info = None  # voice restarted: read the file again
                    continue
                raise RuntimeError(json.load(e).get("error", str(e))) from None
            except urllib.error.URLError:
                if attempt == 0:
                    self.info = None
                    continue
                raise


def ask_model(url, model, system, history):
    req = urllib.request.Request(
        url.rstrip("/") + "/chat/completions",
        data=json.dumps({"model": model, "messages": [{"role": "system", "content": system}] + history}).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=300) as r:
        return json.load(r)["choices"][0]["message"]["content"].strip()


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--name", help="what the user calls this agent; default: the next free character")
    p.add_argument("--project", default=os.path.basename(os.getcwd()))
    p.add_argument("--model", default="qwen2.5:0.5b")
    p.add_argument("--llm", default="http://localhost:11434/v1", help="OpenAI-compatible endpoint")
    p.add_argument("--echo", action="store_true", help="no model: answer with what was heard")
    args = p.parse_args()

    voice = Voice()
    session = f"example-{uuid.uuid4().hex[:8]}"
    reg = voice.call("register", session=session, pid=os.getpid(), client="example-agent",
                     project=args.project, name=args.name or "")
    name = reg["name"]
    persona = reg.get("persona", "")

    def system_prompt(others):
        text = (f"You are {name}, a helpful assistant the user talks to by voice. Answer in one or two short "
                "sentences.")
        if others:
            text += (f" Other agents you can message: {', '.join(others)}. When the user asks you to ask or tell "
                     "one of them something, do not answer it yourself: reply with only @Name: and the message. "
                     f"For example, \"ask {others[0]} what time it is\" becomes \"@{others[0]}: What time is it?\". "
                     "A message from another agent starts with \"Message from\": answer it normally.")
        return text + ("\n\n" + persona if persona else "")
    others = reg.get("others", [])
    print(f"{name} is listening (session {session}). Dictate \"{name}, ...\" with the voice hotkey.", flush=True)

    history = []
    try:
        while True:
            got = voice.call("poll", session=session, state="idle")
            if got.get("persona"):
                persona = got["persona"]
            others = got.get("others", others)
            if not got["messages"]:
                time.sleep(1)
                continue
            heard = " ".join(got["messages"])
            print(f"heard: {heard}", flush=True)
            history.append({"role": "user", "content": heard})
            try:
                reply = heard if args.echo else ask_model(args.llm, args.model, system_prompt(others), history[-12:])
            except Exception as e:  # the model is down: say so instead of dying
                reply = f"I could not reach my model: {e}"
            history.append({"role": "assistant", "content": reply})
            print(f"{name}: {reply}", flush=True)
            to = re.match(r"^\s*@([^\s:,]+)\s*[:,]\s*(.+)", reply, re.S)
            if to and heard.startswith("Message from"):
                # An answer to another agent is said aloud, not sent back:
                # two agents answering each other's answers never stop.
                reply, to = to.group(2).strip(), None
            if to:
                try:
                    voice.call("send", session=session, to=to.group(1), text=to.group(2).strip())
                    reply = f"I passed that on to {to.group(1)}."
                except RuntimeError as e:
                    reply = f"I could not reach {to.group(1)}: {e}"
            voice.call("say", session=session, text=reply)
            voice.call("event", session=session, event="done", text=reply)
    except KeyboardInterrupt:
        pass
    finally:
        try:
            voice.call("end", session=session)
        except Exception:
            pass


if __name__ == "__main__":
    sys.exit(main())
