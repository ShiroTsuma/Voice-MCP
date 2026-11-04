# voice (Claude Code mod)

A Claude Code mod for voice dictation. voice queues what you dictate to a
session (Ctrl+Alt+D). While the session works, voice's hooks pass the message
into the running turn. When the session is idle, this mod takes the message
and submits it as your prompt, so it starts a turn even when the session is
not the one on screen in the Claude app. The status line shows the name you
dictate to the session by.

`/character` picks the character the session plays: `/character Rebekka`
switches at once; without a name it asks. The new character takes over from
your next message.

The mod polls voice's background process every 1.5 s on 127.0.0.1, with the
address and token voice writes to `%APPDATA%\voice\daemon.json` while it runs.
It does nothing while voice is not running.

`voice install` adds it when the `claude` command is on PATH; by hand:

    claude plugin marketplace add <voice>\claude-plugin\voice-dictation
    claude plugin install voice@voice -s user
