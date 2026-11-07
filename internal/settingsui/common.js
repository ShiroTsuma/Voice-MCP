// What every settings window shares: the page's own dialogs, the drawn
// select menus, the settings API call, the [i] tips and language names. A
// window loads it with <script src="/settings/common.js"></script> right before
// its own script (inside <body>), and keeps only what is its own.
//
// Names are functions or var, never const or let: a window that needs its own
// version redefines the function or assigns the var (askYes = ...), where a
// second const would stop its script.

// The page's own dialogs: the browser's confirm and alert would show voice's address and port.
// dialog resolves true for ok; Esc, a click outside or cancel resolve false.
function dialog(text, ok, cancel) {
  return new Promise(done => {
    const bg = document.createElement('div'); bg.className = 'vdlg';
    const box = document.createElement('div'); box.className = 'vdlg-box'; box.setAttribute('role', 'alertdialog'); box.setAttribute('aria-modal', 'true');
    const p = document.createElement('p'); p.textContent = text;
    const row = document.createElement('div'); row.className = 'vdlg-row';
    const key = e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(false); } };
    const close = v => { bg.remove(); document.removeEventListener('keydown', key, true); done(v); };
    const mk = (label, v, cls) => { const b = document.createElement('button'); b.type = 'button'; b.className = cls; b.textContent = label; b.onclick = () => close(v); row.append(b); return b; };
    const no = cancel ? mk(cancel, false, 'vdlg-btn') : null;
    const yes = mk(ok, true, 'vdlg-btn vdlg-ok');
    box.append(p, row); bg.append(box);
    bg.addEventListener('click', e => { if (e.target === bg) close(false); });
    document.body.append(bg); document.addEventListener('keydown', key, true);
    (no || yes).focus(); // a question starts on Cancel, so Enter never deletes by accident
  });
}
var askYes = (text, ok = 'Delete') => dialog(text, ok, 'Cancel');
var tell = text => dialog(text, 'OK');

// A select's list drawn by the page in the window's own look; the select itself stays and keeps its
// value, so the code that fills and reads it is unchanged. Arrow keys on a closed select still step.
(() => {
  let menu = null, owner = null, items = [], act = -1;
  const close = () => { if (!menu) return; menu.remove(); menu = null; owner = null; removeEventListener('resize', close); };
  const pick = i => {
    const it = items[i]; if (!it || it.dis) return;
    const sel = owner; close();
    if (sel.value !== it.value) { sel.value = it.value; sel.dispatchEvent(new Event('input', { bubbles: true })); sel.dispatchEvent(new Event('change', { bubbles: true })); }
    sel.focus();
  };
  const mark = i => {
    if (act >= 0 && items[act]) items[act].el.classList.remove('hl');
    act = i;
    if (items[i]) { items[i].el.classList.add('hl'); items[i].el.scrollIntoView({ block: 'nearest' }); }
  };
  const step = d => { let i = act; for (let n = 0; n < items.length; n++) { i = (i + d + items.length) % items.length; if (!items[i].dis) return mark(i); } };
  function open(sel) {
    close();
    if (sel.disabled || !sel.options.length) return;
    owner = sel; items = []; act = -1;
    menu = document.createElement('div'); menu.className = 'vmenu'; menu.setAttribute('role', 'listbox');
    const add = o => {
      const el = document.createElement('div'); el.className = 'vopt'; el.setAttribute('role', 'option'); el.textContent = o.textContent;
      if (o.disabled) el.setAttribute('aria-disabled', 'true');
      if (o.selected) { el.classList.add('sel'); el.setAttribute('aria-selected', 'true'); }
      const i = items.length; items.push({ el, value: o.value, dis: o.disabled });
      el.addEventListener('mousemove', () => { if (act !== i && !o.disabled) mark(i); });
      el.addEventListener('click', () => pick(i));
      menu.append(el);
    };
    for (const c of sel.children) {
      if (c.tagName === 'OPTGROUP') { const g = document.createElement('div'); g.className = 'vgrp'; g.textContent = c.label; menu.append(g); [...c.children].forEach(add); }
      else if (c.tagName === 'OPTION') add(c);
    }
    document.body.append(menu);
    const r = sel.getBoundingClientRect(), below = innerHeight - r.bottom - 8, above = r.top - 8;
    const down = below >= Math.min(240, menu.scrollHeight) || below >= above;
    menu.style.minWidth = r.width + 'px'; menu.style.maxWidth = Math.max(r.width, innerWidth - r.left - 8) + 'px';
    menu.style.left = Math.max(4, Math.min(r.left, innerWidth - menu.offsetWidth - 4)) + 'px';
    menu.style.maxHeight = Math.max(120, Math.min(320, down ? below : above)) + 'px';
    if (down) menu.style.top = (r.bottom + 4) + 'px'; else menu.style.bottom = (innerHeight - r.top + 4) + 'px';
    mark(Math.max(0, items.findIndex(it => it.el.classList.contains('sel'))));
    addEventListener('resize', close);
  }
  // Open on a press of the select, or Space, Enter, Alt+Down and F4 on it.
  document.addEventListener('mousedown', e => {
    const sel = e.target.closest && e.target.closest('select');
    if (sel && !sel.multiple && sel.size <= 1) { e.preventDefault(); sel.focus(); if (owner === sel) close(); else open(sel); return; }
    if (menu && !menu.contains(e.target)) close();
  }, true);
  document.addEventListener('keydown', e => {
    if (menu) {
      const k = e.key;
      if (k === 'ArrowDown' || k === 'ArrowUp') step(k === 'ArrowDown' ? 1 : -1);
      else if (k === 'Home') { act = -1; step(1); } else if (k === 'End') { act = 0; step(-1); }
      else if (k === 'Enter' || k === ' ') pick(act);
      else if (k === 'Escape' || k === 'Tab') { const s = owner; close(); if (k === 'Escape') s.focus(); else return; }
      else if (k.length === 1) { const i = items.findIndex((it, n) => n > act && !it.dis && it.el.textContent.toLowerCase().startsWith(k.toLowerCase())); const j = i >= 0 ? i : items.findIndex(it => !it.dis && it.el.textContent.toLowerCase().startsWith(k.toLowerCase())); if (j >= 0) mark(j); }
      else return;
      e.preventDefault(); e.stopPropagation(); return;
    }
    const sel = e.target.tagName === 'SELECT' ? e.target : null;
    if (sel && !sel.multiple && (e.key === ' ' || e.key === 'Enter' || e.key === 'F4' || (e.altKey && e.key === 'ArrowDown'))) { e.preventDefault(); open(sel); }
  }, true);
  document.addEventListener('scroll', e => { if (menu && !menu.contains(e.target)) close(); }, true);
  addEventListener('blur', close);
})();

// The settings API: GET without a body, POST with one; an error carries the server's message.
async function api(path, body) {
  const r = await fetch('/settings/api/' + path, body === undefined ? {} :
    { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw Object.assign(new Error(data.error || r.statusText), { status: r.status, data });
  return data;
}

// [i]: its data-tip in a box under it, kept inside the window.
var tipbox = document.createElement('div'); tipbox.id = 'tipbox'; tipbox.hidden = true; tipbox.setAttribute('role', 'tooltip'); document.body.append(tipbox);
var tipFor = null;
// A tip is plain text with a little markup, the same in every window: a blank
// line starts a paragraph, a line "- …" is a point of a list, **…** is bold.
// Built from text nodes, never parsed as HTML.
function renderTip(box, text) {
  box.textContent = '';
  const inline = (el, s) => s.split(/\*\*(.+?)\*\*/).forEach((part, i) => {
    if (!part) return;
    if (i % 2) { const b = document.createElement('b'); b.textContent = part; el.append(b); } else el.append(part);
  });
  for (const block of String(text || '').trim().split(/\n\s*\n/)) {
    let list = null, para = null;
    for (const line of block.split('\n')) {
      const point = line.match(/^\s*[-•]\s+(.*)$/);
      if (point) {
        if (!list) { list = box.appendChild(document.createElement('ul')); para = null; }
        inline(list.appendChild(document.createElement('li')), point[1]);
      } else if (line.trim()) {
        if (!para) { para = box.appendChild(document.createElement('p')); list = null; } else para.append('\n');
        inline(para, line.trim());
      }
    }
  }
}
function showTip(t) {
  tipFor = t;
  renderTip(tipbox, t.dataset.tip); tipbox.hidden = !tipbox.textContent;
  const r = t.getBoundingClientRect(), w = tipbox.offsetWidth, h = tipbox.offsetHeight;
  tipbox.style.left = Math.max(8, Math.min(innerWidth - w - 8, r.left - 12)) + 'px';
  // Under the [i], or above it near the window's bottom, never past the top.
  tipbox.style.top = (r.bottom + 6 + h > innerHeight - 8 ? Math.max(8, r.top - h - 6) : r.bottom + 6) + 'px';
}
document.addEventListener('mouseover', e => { const t = e.target.closest && e.target.closest('.tip'); if (t) showTip(t); });
document.addEventListener('mouseout', e => { if (e.target.closest && e.target.closest('.tip')) tipbox.hidden = true; });
document.addEventListener('focusin', e => { if (e.target.classList && e.target.classList.contains('tip')) showTip(e.target); });
document.addEventListener('focusout', () => { tipbox.hidden = true; });
// Scrolling carries the box along with its [i].
document.addEventListener('scroll', () => { if (!tipbox.hidden && tipFor) showTip(tipFor); }, true);
// A click on [i] inside a label shows it, without opening the list or flipping
// the switch it labels: caught first and kept from the label.
document.addEventListener('click', e => { if (e.target.classList && e.target.classList.contains('tip')) { e.preventDefault(); e.stopPropagation(); showTip(e.target); } }, true);

// A language's English name ("pl" -> "Polish"); "auto" is detected from the text.
var langName = code => { if (code === 'auto') return 'Detect from text'; try { return new Intl.DisplayNames(['en'], { type: 'language' }).of(code) || code; } catch { return code; } };
