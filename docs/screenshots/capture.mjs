// Takes the README's screenshots of the sample pages at an exact viewport and
// 2x scale, through Edge's DevTools protocol (a headless window has a minimum
// width; the protocol's viewport does not). Node 22 or newer (WebSocket).
//
//   node capture.mjs <pages base URL> <out dir> <profile dir>
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const [base, out, profile] = process.argv.slice(2);
mkdirSync(out, { recursive: true });
const edge = ['C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe', 'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe'].find(existsSync);
if (!edge) { console.error('Microsoft Edge not found'); process.exit(1); }
const port = 9300 + Math.floor(Math.random() * 600);
const proc = spawn(edge, ['--headless=new', '--disable-gpu', `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, '--hide-scrollbars', 'about:blank'], { stdio: 'ignore' });
const sleep = ms => new Promise(r => setTimeout(r, ms));
try {
  let targets;
  for (let i = 0; i < 50 && !targets; i++) {
    try { targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json(); } catch { await sleep(200); }
  }
  const ws = new WebSocket(targets.find(t => t.type === 'page').webSocketDebuggerUrl);
  await new Promise(r => ws.addEventListener('open', r));
  let id = 0;
  const waiting = new Map();
  ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (waiting.has(m.id)) { waiting.get(m.id)(m); waiting.delete(m.id); } });
  const send = (method, params = {}) => new Promise(r => { const n = ++id; waiting.set(n, r); ws.send(JSON.stringify({ id: n, method, params })); });

  // name, page, width, height (CSS pixels; the files are twice that).
  const shots = [
    ['settings.png', 'index.html', 380, 780],
    ['speaking-popup.png', 'index.html?popup=1', 380, 298],
    ['settings-transcript.png', 'index.html?shot=transcript', 380, 780],
    ['settings-speaking.png', 'index.html?shot=speaking', 380, 780],
    ['characters.png', 'characters.html?shot=character', 900, 640],
    ['integrations.png', 'integrations.html?shot=gpu-info', 900, 640],
    ['reader.png', 'reader.html', 560, 640],
  ];
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'dark' }] });
  for (const [name, page, width, height] of shots) {
    await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 2, mobile: false });
    await send('Page.navigate', { url: `${base}/settings/${page}` });
    await sleep(2500); // the pages' entry animations and the ?shot clicks
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    writeFileSync(join(out, name), Buffer.from(shot.result.data, 'base64'));
    console.log(`${name} ${width * 2}x${height * 2}`);
  }
  ws.close();
} finally {
  proc.kill();
}
