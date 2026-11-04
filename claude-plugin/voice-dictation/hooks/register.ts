import type { EngineInterface, Register } from 'claude-code'

// voice (the local speech MCP server) queues what the user dictates to a
// session. While a turn runs, voice's hooks pass it into the turn; when the
// session is idle, this mod takes it and submits it as the user's prompt,
// which starts a turn even when the session is not on screen. /character
// picks the character the session plays.

type Daemon = { addr: string; token: string }
type ModResult = { name?: string; messages?: string[]; characters?: string[] }

const POLL_MS = 1500

let daemon: Daemon | undefined
let idle = true // a new session waits for its first prompt
let polling = false
let shown: string | undefined
// Set while a question dialog is open: poll hands it what the user dictates.
let answerWith: ((text: string) => void) | undefined

// voice writes its address and token to voice\daemon.json in the user's
// config folder while it runs.
async function discover($: EngineInterface): Promise<Daemon | undefined> {
  const appData = await $.env.get('APPDATA')
  const xdg = await $.env.get('XDG_CONFIG_HOME')
  const home = await $.env.get('HOME')
  const path = appData
    ? `${appData}\\voice\\daemon.json`
    : `${xdg || `${home}/.config`}/voice/daemon.json`
  try {
    const info = JSON.parse(await $.fs.read(path)) as Partial<Daemon>
    return info.addr && info.token ? { addr: info.addr, token: info.token } : undefined
  } catch {
    return undefined // voice is not running
  }
}

// ask calls voice's mod endpoint for this session; undefined when voice is
// not running. An error voice reports (an unknown character) is thrown.
async function ask($: EngineInterface, extra: { idle?: boolean; character?: string; question?: boolean; say?: string }): Promise<ModResult | undefined> {
  daemon ??= await discover($)
  if (!daemon) return undefined
  const session = await $.session.id()
  const res = await $.http.fetch(`http://${daemon.addr}/v1/mod`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Voice-Token': daemon.token },
    body: JSON.stringify({ session, idle: false, ...extra }),
  })
  const body = JSON.parse(res.text || '{}') as { result?: ModResult; error?: string }
  if (res.status === 422) throw new Error(body.error ?? 'voice refused it')
  if (!res.ok) {
    daemon = undefined // restarted with another port or token: look again
    return undefined
  }
  return body.result
}

function status($: EngineInterface, text: string | undefined) {
  if (text !== shown) {
    shown = text
    $.ui.status(text)
  }
}

async function poll($: EngineInterface) {
  if (polling) return
  polling = true
  try {
    // An open question takes the dictation now, though the turn runs.
    const result = await ask($, answerWith ? { idle, question: true } : { idle })
    if (answerWith && (result?.messages?.length ?? 0) > 0) {
      answerWith(result!.messages!.join(' '))
      status($, result?.name || undefined)
      return
    }
    status($, result?.name || undefined)
    const messages = result?.messages ?? []
    if (messages.length > 0 && idle) {
      idle = false
      // The user's own words, dictated: the model reads them as the user's,
      // not as a message from this plugin.
      void $.prompt.submit({ text: messages.join(' '), asUser: true })
    }
  } catch {
    daemon = undefined
    status($, undefined)
  } finally {
    polling = false
  }
}

// pick asks which character the session becomes: the dialog takes four
// options, and "Other" takes any name typed.
async function pick($: EngineInterface, current: string, names: string[]): Promise<string> {
  const others = names.filter(n => n.toLowerCase() !== current.toLowerCase())
  if (others.length === 0) return ''
  if (others.length === 1) return others[0]!
  return $.ui.ask(`Which character should this session be? It is ${current || 'nobody'} now.`, {
    header: 'Character',
    options: others.slice(0, 4),
  })
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const out = await next(e)
    $.clock.every(POLL_MS, () => void poll($))
    return out
  })

  // /character Rebekka switches at once; /character alone asks. The command is
  // commands/character.md, so the app lists it; this answers it without a turn
  // (the file's prompt is for a session that runs without the mod).
  on('command.run', async ($, e, next) => {
    if (e.command !== 'character' && e.command !== 'voice:character') return next(e)
    try {
      const now = await ask($, {})
      if (!now) return { text: 'voice is not running.' }
      const names = now.characters ?? []
      if (names.length === 0) return { text: 'voice has no characters; add some in its Characters window.' }
      const typed = names.find(n => n.toLowerCase() === e.args.trim().toLowerCase())
      const choice = typed ?? (await pick($, now.name ?? '', names))
      if (!choice) return { text: `This session is ${now.name}, the only character there is.` }
      const done = await ask($, { character: choice })
      status($, done?.name || undefined)
      return { text: `This session is ${done?.name} now; the new character takes over from your next message.` }
    } catch (err) {
      return { text: (err as Error).message }
    }
  })

  // A question the agent puts to the user (AskUserQuestion) waits for a click;
  // what the user dictates meanwhile answers it instead, as their own words
  // (the dialog's "Other"). The dialog shows as usual and a click still
  // answers it; whichever comes first is the answer.
  on('tool.call', { tool: 'AskUserQuestion' }, async ($, e, next) => {
    const first = e.questions?.[0]?.question
    if (!first) return next(e)
    // Read aloud (voice does, with its "answer by voice" setting on): the
    // question and its choices, so it can be answered without the screen.
    const labels = (e.questions?.[0]?.options ?? []).map(o => o.label)
    const say = labels.length ? `${first} ${labels.join(', ')}, or something else.` : first
    void ask($, { say }).catch(() => undefined)
    const clicked = next(e)
    const said = new Promise<string>(resolve => { answerWith = resolve })
    try {
      const won = await Promise.race([clicked.then(r => ({ r })), said.then(text => ({ text }))])
      if ('r' in won) return won.r
      return { result: { questions: e.questions, answers: { [first]: won.text } } }
    } finally {
      answerWith = undefined
    }
  })

  // Only the main loop's turns count: a subagent's run says nothing about
  // whether the session waits for the user.
  on('turn.start', ($, e, next) => {
    if (e.agentId === undefined) idle = false
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    const out = await next(e)
    if (e.agentId === undefined) {
      idle = true
      void poll($) // what was dictated during the turn's last step goes at once
    }
    return out
  })
}
