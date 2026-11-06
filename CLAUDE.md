# Before every commit

Go through these for what the commit changes, and fix what fails before committing.

## MCP and REST stay one surface
- An action, argument or result field added to an MCP tool (`internal/mcpserver`) works the same over HTTP: through the daemon's `/v1/<tool>` endpoint, which passes the same service arguments, and, when it belongs to the documented Session API (`/v1/session/...`), in `docs/http-api.md` too.
- New service argument fields decode from the same JSON names on both paths; nothing the MCP schema hides (client, pid, project) becomes settable over HTTP by accident.

## Models and GPU backends: NVIDIA is not the only card
- Anything that uses a model or a GPU backend: does it run on AMD (ROCm on Windows) and on a PC with no supported GPU? `sidecar/backends.json` says per backend (`gpus`); a backend listed for nvidia only does not exist for an AMD user.
- If it does not run there, there is a handling: the feature is off or hidden with the reason ("needs an NVIDIA graphics card"), never a hint to install something that cannot be installed on that PC, and a CPU or other-card fallback where one exists.
- Defaults and automatic choices (an engine picked for the user, a preset, a model) never assume NVIDIA or an installed GPU backend.

## UI: consistent, and right for a fresh PC
- A window or section is built like the others: the Characters window's layout for list-and-detail pages, the shared controls (rows, switches, select menus drawn by the page, dialogs), one short line under a setting and the full explanation behind an [i].
- Nothing assumes this PC: a fresh install on AMD or without a GPU, with no cloned voices, no characters of the user's own, no Qwen3-TTS, Chatterbox or other GPU backend, another language than English. Empty and unavailable states say what to do, and only what can be done on that PC.
- A list the page edits is saved as one revision and refreshed when shown again; another window or an agent may have changed it.
