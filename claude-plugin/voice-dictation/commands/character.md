---
description: Pick the voice character this session plays
argument-hint: [name]
---
Voice character: "$ARGUMENTS".
If that is empty or not a character's name, call the voice MCP tool voice_control with action "list_characters", show me the names with their short descriptions, and ask which one I want; once I answer, go on as below with that name.
Call voice_control with action "set_character" and character set to the name, then reply in one short sentence in that character.