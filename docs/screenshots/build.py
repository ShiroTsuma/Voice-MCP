"""Builds a static copy of voice's settings pages with sample data, for the
README's screenshots (see README.md here; make.ps1 runs it).

    python build.py <work dir> <repo root>

<work dir> holds state.json, status.json, integrations.json and themes.json as
the running voice answered them. Only their shape is kept: every name,
project, path, device, voice and line shown is sample data written below.
Before writing anything, the page data is checked against what the live data
says about this PC (sessions, projects, characters, cloned voices, devices,
the user's name and folders): one match stops the build.
"""
import json
import os
import re
import shutil
import sys

work, repo = sys.argv[1], sys.argv[2]
load = lambda n: json.load(open(os.path.join(work, n + ".json"), encoding="utf-8-sig"))
state, status, integ, themes = load("state"), load("status"), load("integrations"), load("themes")

# What must never reach a screenshot: what the live data says about this PC.
private = set()
for a in (state.get("agents") or []) + (status.get("agents") or []):
    private.update(x for x in (a.get("name"), a.get("project")) if x)
for c in state.get("characters") or []:
    private.add(c.get("name") or "")
# A cloned or designed voice is the user's: flagged "cloned" (its description
# is the user's own words); older voices only said so in the description.
cloned = lambda p: p.get("cloned") or (p.get("description") or "").startswith("Cloned voice")
for p in state["options"].get("presets") or []:
    if cloned(p):
        private.add(p["name"])
        private.add(p.get("description") or "")
private.update(state["options"].get("inputs") or [])
private.update(state["options"].get("outputs") or [])
private.add(state.get("listen", {}).get("input_device") or "")
if os.environ.get("USERNAME"):
    private.add("\\\\Users\\\\" + os.environ["USERNAME"])  # a home folder, as JSON writes it
private.add(themes.get("dir") or "")

# Sample names, and the project's own public name (its links name it; a
# session working on voice itself has it as its project).
SAMPLE = {"Anna", "Clara", "Rebekka", "Diana", "Fiona", "Greta", "narrator", "shop-api", "notes-app", "website", "Voice-MCP"}

# The state: settings shape, sample people.
state["version"] = os.environ.get("SHOT_VERSION") or state["version"].split("-dev")[0]
state["languages"] = ["en"]
state["default_preset"] = "calm-mature"
state["notes"] = None
state["listen"]["input_device"] = "Microphone (USB Audio)"
opts = state["options"]
opts["inputs"] = ["Microphone (USB Audio)", "Headset Microphone"]
opts["outputs"] = ["Speakers (Realtek Audio)", "Headphones"]
opts["presets"] = [p for p in opts["presets"] if not cloned(p)
                   # a tuning of the user's voice is theirs too
                   and not any(c["name"] == p.get("extends") and cloned(c) for c in opts["presets"])]
opts["presets"].append({"description": "Deep, warm storyteller (designed from a description)", "dsp": None, "engine": "qwen3", "name": "narrator", "pitch": 0, "speed": 1, "cloned": True})
state["characters"] = [
    {"name": "Anna", "traits": ["calm", "witty"], "description": "Dry humour, gets to the point.", "preset": "calm-mature", "color": "#7fd1a8"},
    {"name": "Clara", "traits": ["cheerful", "enthusiastic"], "description": "Bright and quick; loves a clean diff.", "preset": "neutral", "color": "#8ab4f8"},
    {"name": "Rebekka", "traits": ["formal", "blunt"], "description": "Precise, a little stern, never wordy.", "preset": "brisk", "color": "#e5c07b"},
    {"name": "Diana", "traits": ["playful", "teasing"], "description": "Teases, then helps anyway.", "preset": "neutral", "color": "#f28fad"},
    {"name": "Fiona", "traits": ["shy", "sweet"], "description": "Soft-spoken and careful.", "preset": "calm-mature", "color": "#c3a6ff"},
    {"name": "Greta", "traits": ["grumpy"], "description": "Complains, ships anyway.", "preset": "brisk", "color": "#f0a868"},
]
state["agents"] = []
state["transcript"] = []
state["gpu"] = None  # the GPU voices' memory names this PC's voices and the characters using them

# Integrations: the real list, with this PC's paths, jobs and server made neutral.
def neutral(s):
    s = re.sub(r"[A-Za-z]:\\[^ ;,)]*", r"%LOCALAPPDATA%\\voice", s)
    return s
for it in integ["items"]:
    st = it["status"]
    it["job"] = None
    if st.get("detail"):
        st["detail"] = neutral(st["detail"])
    if st.get("field"):
        st["field"]["value"] = "http://127.0.0.1:8000"
    if it["id"] == "higgs":
        st.update({"state": "missing", "label": "", "detail": "Its server does not answer at http://127.0.0.1:8000. Set one up (Setup guide) on this PC, another machine or a server, and put its address here; until then another voice speaks"})
        st["actions"] = [{"id": "start", "label": "Start server", "primary": True}, {"id": "guide", "label": "Setup guide"}]
integ["languages"] = ["en"]
themes["dir"] = r"C:\Users\you\AppData\Local\voice\data\themes"

site = os.path.join(work, "site")
shutil.rmtree(site, ignore_errors=True)
os.makedirs(os.path.join(site, "settings"))
# The reading window's story theme, when it is installed or in the role-play package.
for src in (os.path.join(repo, "roleplay", "themes", "storybook", "theme.css"), os.path.join(work, "storybook.css")):
    if os.path.exists(src):
        os.makedirs(os.path.join(site, "settings", "theme", "storybook"))
        shutil.copy(src, os.path.join(site, "settings", "theme", "storybook", "theme.css"))
        break

mock = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mock.js"), encoding="utf-8").read()
mock = mock.replace("/*STATE*/null", json.dumps(state)).replace("/*INTEGRATIONS*/null", json.dumps(integ)).replace("/*THEMES*/null", json.dumps(themes))

# Nothing about this PC may be in the pages' data.
# As whole words: a character called Vera is not in "several".
shown = lambda p: re.search(r"(?<![A-Za-z0-9])" + re.escape(p) + r"(?![A-Za-z0-9])", mock, re.I)
leaks = sorted(p for p in private if p and p not in SAMPLE and len(p) > 2 and shown(p))
if leaks:
    sys.exit("private data would be shown: " + ", ".join(leaks))

with open(os.path.join(site, "mock.js"), "w", encoding="utf-8") as f:
    f.write(mock)
pages = {"index.html": "panel.html", "characters.html": "characters.html", "integrations.html": "integrations.html", "reader.html": "reader.html"}
for out, src in pages.items():
    html = open(os.path.join(repo, "internal", "settingsui", src), encoding="utf-8").read()
    pre = '<script src="/mock.js"></script>'
    if src == "reader.html":
        pre = "<script>window.__readerMode = true;</script>" + pre
    with open(os.path.join(site, "settings", out), "w", encoding="utf-8") as f:
        f.write(html.replace("<head>", "<head>" + pre, 1))
print("sample pages in", site)
