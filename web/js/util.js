// DOM helpers, formatting, api(), toast, dialogs

// ───────────────────────── utilities ─────────────────────────
export const $ = (s, el = document) => el.querySelector(s);
export const $$ = (s, el = document) => [...el.querySelectorAll(s)];
export const uid = () => Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
export const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
export const fmtBytes = n => { n = Number(n); if (!Number.isFinite(n)) return ''; const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0; while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; } return (i ? n.toFixed(1) : n) + ' ' + u[i]; };
export const fmtCtx = n => n >= 1024 && n % 1024 === 0 ? (n / 1024) + 'K' : fmtK(n);
export const fmtK = n => n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + 'k' : String(n);
export const debounce = (fn, ms) => { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };
export const domainOf = u => { try { return new URL(u).hostname.replace(/^www\./, ''); } catch { return u; } };
const hue = s => { let h = 0; for (const c of s) h = (h * 31 + c.charCodeAt(0)) % 360; return h; };
export const fav = u => { const d = domainOf(u); return `<span class="fav" style="background:hsl(${hue(d)} 55% 45%)">${esc(d[0] || '?')}</span>`; };

const sv = (d, extra = '') => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" ${extra}>${d}</svg>`;
const GUANACO = 'M7.5 23V14C7.5 11.4 7.9 9.3 8.8 7.6L8.2 3.3C8.1 2.7 8.8 2.4 9.2 2.9L10.8 5.5L11.6 2.6C11.8 2 12.6 2 12.7 2.7L13.1 6.2C14.9 6.7 16.3 7.8 17.5 8.8L19.6 9.9C20.6 10.4 20.5 11.9 19.4 12.2L16.2 13.1C14.9 13.5 14.2 14.5 14.2 16V23Z';
const ICONS = {
  guanacoMark: `<svg viewBox="0 0 24 24"><defs><linearGradient id="llg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#53d9ff"/><stop offset="1" stop-color="#b57bff"/></linearGradient></defs><path d="${GUANACO}" fill="url(#llg)"/><circle cx="13.6" cy="9.3" r=".95" class="guanaco-eye"/></svg>`,
  guanaco: sv(`<path d="${GUANACO}"/><path d="M13.6 9.3h.01" stroke-width="2.4"/>`),
  plus: sv('<path d="M12 5v14M5 12h14"/>'),
  panel: sv('<rect x="3" y="4" width="18" height="16" rx="3"/><path d="M9 4v16"/>'),
  chat: sv('<path d="M21 12a8 8 0 0 1-11.8 7L4 20l1-4.6A8 8 0 1 1 21 12z"/>'),
  sliders: sv('<path d="M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12"/><circle cx="16" cy="6" r="2"/><circle cx="10" cy="12" r="2"/><circle cx="18" cy="18" r="2"/>'),
  search: sv('<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>'),
  bookmark: sv('<path d="M6 3h12v18l-6-4-6 4z"/>'),
  trash: sv('<path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/>'),
  box: sv('<path d="M21 8 12 3 3 8v8l9 5 9-5z"/><path d="m3 8 9 5 9-5M12 13v8"/>'),
  gear: sv('<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>'),
  download: sv('<path d="M12 3v12M7 10l5 5 5-5M5 21h14"/>'),
  file: sv('<path d="M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8z"/><path d="M14 3v5h5M9 13h6M9 17h6"/>'),
  folder: sv('<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>'),
  code: sv('<path d="m8 6-6 6 6 6M16 6l6 6-6 6"/>'),
  copy: sv('<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>'),
  check: sv('<path d="M5 12l5 5L20 7"/>'),
  globe: sv('<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>'),
  bulb: sv('<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0 0 12 3z"/>'),
  chev: sv('<path d="m6 9 6 6 6-6"/>'),
  chevr: sv('<path d="m9 6 6 6-6 6"/>'),
  trash2: sv('<path d="M4 7h16M6 7l1 13h10l1-13M9 7V4h6v3"/>'),
  volume: sv('<path d="M11 5 6 9H3v6h3l5 4z"/><path d="M15.5 8.5a5 5 0 0 1 0 7M18.5 5.5a9 9 0 0 1 0 13"/>'),
  mic: sv('<rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5 11a7 7 0 0 0 14 0M12 18v3"/>'),
  book: sv('<path d="M4 5a2 2 0 0 1 2-2h13v16H6a2 2 0 0 0-2 2z"/><path d="M4 21V5M9 8h6M9 12h6"/>'),
  edit2: sv('<path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/>'),
  chevl: sv('<path d="m15 6-6 6 6 6"/>'),
  columns: sv('<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M12 4v16"/>'),
  branch: sv('<circle cx="6" cy="5.5" r="2.2"/><circle cx="6" cy="18.5" r="2.2"/><circle cx="18" cy="8.5" r="2.2"/><path d="M6 7.7v8.6M18 10.7c0 4.3-6 3.3-11 6.3"/>'),
  up: sv('<path d="M12 19V5M5 12l7-7 7 7"/>', 'stroke-width="2.2"'),
  stop: '<svg viewBox="0 0 24 24"><rect x="7" y="7" width="10" height="10" rx="2" fill="currentColor"/></svg>',
  down: sv('<path d="M12 5v14M5 12l7 7 7-7"/>'),
  term: sv('<path d="m4 17 6-5-6-5M12 19h8"/>'),
  upload: sv('<path d="M12 16V4M7 9l5-5 5 5M5 20h14"/>'),
  x: sv('<path d="M6 6l12 12M18 6 6 18"/>'),
  more: sv('<circle cx="5" cy="12" r="1.3" fill="currentColor"/><circle cx="12" cy="12" r="1.3" fill="currentColor"/><circle cx="19" cy="12" r="1.3" fill="currentColor"/>'),
  star: sv('<path d="M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1L3.2 9.5l6.1-.9z"/>'),
  starf: sv('<path fill="currentColor" d="M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1L3.2 9.5l6.1-.9z"/>'),
  refresh: sv('<path d="M21 12a9 9 0 1 1-2.6-6.4L21 8M21 3v5h-5"/>'),
  edit: sv('<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>'),
  alert: sv('<circle cx="12" cy="12" r="9"/><path d="M12 8v5M12 16h.01"/>'),
  image: sv('<rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="2"/><path d="m21 16-5-5-9 9"/>'),
  cloud: sv('<path d="M7 18a4.5 4.5 0 0 1-.6-8.96A6 6 0 0 1 18 9.5a4 4 0 0 1-.5 8.5z"/>'),
  chip: sv('<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M10 10h4v4h-4zM9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4"/>'),
  plug: sv('<path d="M9 2v5M15 2v5M6 7h12v4a6 6 0 0 1-12 0zM12 17v5"/>'),
  key: sv('<circle cx="7.5" cy="15.5" r="4.5"/><path d="m10.7 12.3 9.3-9.3M17 6l3 3M14.5 8.5l2 2"/>'),
  zap: sv('<path d="M13 2 4 14h7l-1 8 9-12h-7z"/>'),
};
export const ic = n => ICONS[n] || '';
export function hydrateIcons(root = document) {
  root.querySelectorAll('[data-icon]').forEach(el => {
    if (el.dataset.iconDone) return;
    el.insertAdjacentHTML('afterbegin', ic(el.dataset.icon));
    el.dataset.iconDone = '1';
  });
}

export async function api(path, opts = {}) {
  const r = await fetch('/api' + path, { ...opts, headers: { 'X-Guanaco': '1', 'Content-Type': 'application/json', ...(opts.headers || {}) } });
  if (!r.ok) { let m; try { m = (await r.json()).error; } catch { } throw new Error(m || r.status + ' ' + r.statusText); }
  if (r.status === 204) return null;
  return (r.headers.get('content-type') || '').includes('json') ? r.json() : r.text();
}

export function toast(msg, err = false) {
  const t = document.createElement('div');
  t.className = 'toast' + (err ? ' err' : '');
  t.textContent = msg;
  $('#toasts').appendChild(t);
  setTimeout(() => t.remove(), err ? 5000 : 2600);
}

export function ask({ title, msg = '', value = '', input = true, okText = 'OK', danger = false }) {
  return new Promise(res => {
    const m = $('#promptModal'), inp = $('#promptInput'), ok = $('#promptOk');
    $('#promptTitle').textContent = title; $('#promptMsg').textContent = msg;
    $('#promptMsg').classList.toggle('hidden', !msg);
    inp.classList.toggle('hidden', !input); inp.value = value;
    ok.textContent = okText; ok.classList.toggle('danger', danger);
    m.classList.remove('hidden');
    if (input) setTimeout(() => { inp.focus(); inp.select(); }, 20); else setTimeout(() => ok.focus(), 20);
    const done = v => { m.classList.add('hidden'); ok.onclick = $('#promptCancel').onclick = inp.onkeydown = null; res(v); };
    ok.onclick = () => done(input ? inp.value : true);
    $('#promptCancel').onclick = () => done(null);
    inp.onkeydown = e => { if (e.key === 'Enter') done(inp.value); if (e.key === 'Escape') done(null); };
  });
}
