// Sample data for the README's screenshots: build.py fills in the settings'
// shape and the pages load this before their own script. It answers their
// API calls; nothing here is anyone's real session.
(() => {
const now = Date.now(), at = s => new Date(now - s * 1000).toISOString();
const STATE = /*STATE*/null, INTEG = /*INTEGRATIONS*/null, THEMES = /*THEMES*/null;
const agents = [
  {pid: 101, name: 'Anna', client: 'claude', project: 'shop-api', busy: true, waiting: 0, active: at(2)},
  {pid: 102, name: 'Clara', client: 'codex', project: 'notes-app', busy: false, waiting: 0, active: at(70)},
  {pid: 103, name: 'Rebekka', client: 'claude', project: 'website', busy: true, waiting: 1, active: at(30), no_speech: true},
];
const transcript = [
  {at: at(600), id: 'v1', kind: 'said', pid: 103, name: 'Rebekka', client: 'claude', project: 'website', source: 'hook', text: 'website. The landing page builds again; the hero image is 40 percent smaller.'},
  {at: at(420), id: 'v2', kind: 'dictated', pid: 103, name: 'Rebekka', client: 'claude', project: 'website', text: 'Rebekka, compress the rest of the gallery too.', outcome: 'Delivered to Rebekka'},
  {at: at(240), id: 'v3', kind: 'said', pid: 102, name: 'Clara', client: 'codex', project: 'notes-app', source: 'hook', text: 'notes-app. Dark theme is in, with a toggle in settings. Want me to make it the default?'},
  {at: at(90), id: 'v4', kind: 'said', pid: 101, name: 'Anna', client: 'claude', project: 'shop-api', text: 'The migration ran cleanly on staging.'},
  {at: at(60), id: 'v5', kind: 'dictated', pid: 101, name: 'Anna', client: 'claude', project: 'shop-api', text: 'And skip the slow integration ones for now.', outcome: 'Queued for Anna, busy; it gets it with its next step'},
];
// Anna is saying "Should": the words before it are said, the rest still dim.
const said = 'Two tests fail, both in the checkout module. Should I fix them now?';
const from = said.indexOf('Should');
const line = {id: 'v6', pid: 101, name: 'Anna', client: 'claude', project: 'shop-api', text: said, from, to: from + 6, done: from + 1, at: at(0.2), seconds: 1e9};
STATE.agents = agents; STATE.transcript = transcript;
const lines = [
  {voice: 'narrator', kind: 'narration', text: 'The kitchen was quiet, except for the clock and one plate with a single slice of cake.'},
  {speaker: 'Clara', voice: 'neutral', text: 'Okay. Who ate the other three?', mood: 'amused', to: 'everyone'},
  {speaker: 'Diana', voice: 'neutral', text: "Don't look at me like that. I was in my room the whole time.", mood: 'teasing', action: 'crosses her arms'},
  {speaker: 'Fiona', voice: 'calm-mature', text: 'I only had a tiny piece. Okay, maybe two.', mood: 'sad'},
];
const scene = {at: at(30), id: 'v7', kind: 'said', pid: 102, name: 'Clara', client: 'codex', project: 'notes-app', scene: true, style: 'narrative',
  setting: {title: 'The last slice', location: 'Kitchen', time: 'Late evening'}, lines,
  text: lines.map(l => (l.speaker || 'Narrator') + ': ' + l.text).join(' ')};
const SAMPLES = {
  state: STATE,
  status: {agents, transcript, muted: false, preset: 'calm-mature', reader_pinned: false, speaking: true, line},
  integrations: INTEG,
  themes: THEMES,
  wordlists: STATE.wordlists || [],
};
const real = window.fetch.bind(window);
window.fetch = async (url, opts) => {
  const m = String(url).match(/\/settings\/api\/([a-z_]+)/);
  if (!m) return real(url, opts);
  let body = SAMPLES[m[1]];
  if (opts && opts.method === 'POST' && body === undefined) body = {};
  // The reading window, opened with its hotkey, on a session telling a story.
  if (window.__readerMode && (m[1] === 'status' || m[1] === 'state')) {
    const story = agents.map(a => a.pid === 102 ? Object.assign({}, a, {style: 'narrative'}) : a);
    body = Object.assign({}, body, {transcript: [transcript[2], scene], line: null, speaking: false, reader_pinned: true, agents: story});
  }
  return new Response(JSON.stringify(body === undefined ? {} : body), {status: 200, headers: {'Content-Type': 'application/json'}});
};
// ?shot=...: what a screenshot shows beyond a page's first view (a headless browser does not click).
const shot = new URLSearchParams(location.search).get('shot');
if (shot) addEventListener('load', () => setTimeout(() => {
  const nav = name => [...document.querySelectorAll('nav button')].find(b => b.textContent.includes(name));
  if (shot === 'gpu-info') { nav('GPU').click(); setTimeout(() => window.openInfo && openInfo('chatterbox_turbo'), 100); }
  if (shot === 'character') { const b = [...document.querySelectorAll('.item')].find(e => e.textContent.trim().startsWith('Clara')); if (b) b.click(); }
  if (shot === 'transcript' || shot === 'speaking') {
    const want = shot === 'transcript' ? 'Transcript' : 'Speaking';
    const row = [...document.querySelectorAll('main button, main a, main [role=button], main .row, main li')].find(e => e.textContent.trim().startsWith(want));
    if (row) row.click();
  }
}, 600));
})();
