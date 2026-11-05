// Side-by-side model comparison
import { forkNav } from './branches.js';
import { saveChat } from './chats.js';
import { fileCard } from './composer.js';
import { linkCitations, renderMD, splitThink } from './markdown.js';
import { capKey } from './models.js';
import { isLocal, provName } from './providers.js';
import { isNearBottom, scrollToBottom } from './render.js';
import { S } from './state.js';
import { toolCard } from './tools.js';
import { $, domainOf, esc, fav, fmtCtx, fmtK, ic } from './util.js';
import { SPEAK_BTN } from './voice.js';

// ── side-by-side compare ──
// A compare reply is one message with m.cols = [colA, colB] (each a normal assistant object) and m.pick.
// The picked column is mirrored onto the message itself, so history, export and backups just see one answer.
export function syncPick(m) {
  const c = m.cols[m.pick] || m.cols[0];
  m.content = c.content || ''; m.provider = c.provider; m.model = c.model;
  for (const k of ['thinking', 'thinkMs', 'stats', 'error', 'stopped', 'sources', 'searchQuery']) { if (c[k] !== undefined) m[k] = c[k]; else delete m[k]; }
}
export function buildCompare(el, m) {
  el.classList.add('cmp');
  el.innerHTML = `<div class="cmp-grid"></div><div class="msg-actions cmp-foot"><span class="fork-slot"></span><button class="icon-btn" data-act="regen" title="Run both models again">${ic('refresh')}</button><button class="icon-btn" data-act="branch" title="Branch into a new chat from here">${ic('branch')}</button></div>`;
  const grid = $('.cmp-grid', el);
  m.cols.forEach(c => {
    const ce = document.createElement('div');
    ce.className = 'cmp-col'; ce.dataset.id = c.id; ce._host = m;
    grid.appendChild(ce);
    renderAssistant(ce, c, m);
  });
  updateCmpFoot(el, m);
}
function updateCmpFoot(el, m) {
  const foot = $('.cmp-foot', el); if (!foot) return;
  foot.classList.toggle('hidden', m.cols.some(c => c._streaming));
  $('.fork-slot', foot).innerHTML = forkNav(m);
}
// Re-render a message after it changed (works for plain and compare messages).
export function refreshMsg(m) {
  const el = $(`#messages .msg[data-id="${m.id}"]`); if (!el) return;
  if (!m.cols) { renderAssistant(el, m); return; }
  m.cols.forEach(c => { const ce = $(`.cmp-col[data-id="${c.id}"]`, el); if (ce) renderAssistant(ce, c, m); });
  updateCmpFoot(el, m);
}
export function pickCol(chat, host, col) {
  if (S.streams.has(chat.id) || chat.messages.at(-1) !== host) return;
  host.pick = host.cols.indexOf(col);
  syncPick(host); refreshMsg(host); saveChat(chat, false);
}

export function renderUser(el, m) {
  const atts = m.attachments || [];
  let imgIdx = 0;
  const attHtml = atts.map(a => {
    if (a.kind === 'image') { const src = `data:${a.mime || 'image/png'};base64,${m.images?.[imgIdx++] || ''}`; return `<img class="att-img" src="${esc(src)}" alt="${esc(a.name)}">`; }
    return fileCard(a);
  }).join('');
  el.innerHTML = `<div class="user-col">${attHtml ? `<div class="att-row">${attHtml}</div>` : ''}
    ${m.content ? `<div class="bubble">${esc(m.content)}</div>` : ''}
    <div class="msg-actions">${forkNav(m)}<button class="icon-btn" data-act="copy" title="Copy">${ic('copy')}</button><button class="icon-btn" data-act="edit" title="Edit">${ic('edit')}</button></div></div>`;
}

function statsLine(m) {
  const s = m.stats, bits = [];
  const prov = m.provider || 'ollama', local = isLocal(prov);
  if (m.model) bits.push(m.model);
  if (prov !== 'ollama') bits.push(provName(prov));
  if (s?.eval_count) bits.push(`${s.eval_count} tok`);
  if (s?.tps) bits.push(`${s.tps.toFixed(1)} tok/s`);
  if (s && (s.prompt_eval_count || s.eval_count)) {
    if (prov === 'ollama') {
      const used = (s.prompt_eval_count || 0) + (s.eval_count || 0), max = Number(S.settings.params.num_ctx) || S.caps[capKey(prov, m.model)]?.ctx;
      bits.push(`ctx ${fmtK(used)}${max ? '/' + fmtCtx(max) : ''}`);
    } else if (s.prompt_eval_count) bits.push(`${fmtK(s.prompt_eval_count)} in`);
  }
  if (!local && s?.load_ms) bits.push(`${(s.load_ms / 1000).toFixed(1)}s to first token`);
  if (m.stopped) bits.push('stopped');
  if (s?.done_reason === 'length') bits.push('hit token limit');
  return bits.join(' · ');
}

export function renderAssistant(el, m, host) {
  const streaming = !!m._streaming;
  let content = m.content || '', thinking = m.thinking || '', thinkOpenTag = false;
  const st = splitThink(content);
  if (st) { thinking = (thinking ? thinking + '\n' : '') + st.thinking; content = st.content; thinkOpenTag = st.open; }
  const isThinking = streaming && (thinkOpenTag || (thinking && !content));

  if (!el._p) {
    el.innerHTML = `${host ? '<div class="cmp-head"></div>' : ''}<div class="p-src"></div><div class="p-docs"></div><div class="p-status"></div><div class="p-think"></div><div class="prose"></div><div class="p-tools"></div><div class="p-err"></div>
      <div class="msg-actions"><span class="fork-slot"></span><button class="icon-btn" data-act="copy" title="Copy">${ic('copy')}</button>${SPEAK_BTN()}${host
        ? '<button class="btn sm pick" data-act="pick"></button>'
        : `<button class="icon-btn" data-act="regen" title="Regenerate with the selected model">${ic('refresh')}</button><button class="icon-btn" data-act="branch" title="Branch into a new chat from here">${ic('branch')}</button>`}<span class="stats"></span></div>`;
    el._p = { head: $('.cmp-head', el), fork: $('.fork-slot', el), pick: $('.pick', el), src: $('.p-src', el), docs: $('.p-docs', el), status: $('.p-status', el), think: $('.p-think', el), prose: $('.prose', el), tools: $('.p-tools', el), err: $('.p-err', el), actions: $('.msg-actions', el), stats: $('.stats', el) };
  }
  const p = el._p;

  if (m.sources?.length) {
    if (!p.src.firstChild || p.src.dataset.n != m.sources.length) {
      p.src.dataset.n = m.sources.length;
      p.src.innerHTML = `<div class="sources${m._srcOpen ? ' open' : ''}"><button class="src-toggle" data-act="sources"><span class="favs">${m.sources.slice(0, 4).map(s => fav(s.url)).join('')}</span>${m.sources.length} sources${m.searchQuery ? ` · “${esc(m.searchQuery.length > 50 ? m.searchQuery.slice(0, 50) + '…' : m.searchQuery)}”` : ''}</button>
        <div class="src-list">${m.sources.map((s, i) => `<a class="src-card" href="${esc(s.url)}" target="_blank" rel="noopener noreferrer"><span class="dom">${fav(s.url)}${i + 1} · ${esc(domainOf(s.url))}</span><span class="ttl">${esc(s.title)}</span></a>`).join('')}</div></div>`;
    }
  } else p.src.innerHTML = '';

  const dk = (m.docs?.length || 0) + '|' + (m.docsNote || '');
  if (p.docs.dataset.k !== dk) {
    p.docs.dataset.k = dk;
    const names = [...new Set((m.docs || []).map(d => d.doc))];
    p.docs.innerHTML = (m.docs?.length ? `<div class="sources docs${m._docsOpen ? ' open' : ''}"><button class="src-toggle" data-act="docs"><span class="favs doc-ic">${ic('file')}</span>${m.docs.length} passage${m.docs.length === 1 ? '' : 's'} from ${esc(names.length > 2 ? names.length + ' documents' : names.join(', '))}</button>
        <div class="src-list">${m.docs.map((d, i) => `<div class="src-card"><span class="dom">D${i + 1} · ${esc(d.doc)} · ${Math.round((d.score || 0) * 100)}%</span><span class="ttl doc-ttl">${esc(d.text.length > 280 ? d.text.slice(0, 280) + '…' : d.text)}</span></div>`).join('')}</div></div>` : '')
      + (m.docsNote ? `<div class="docs-note">${ic('alert')}<span>${esc(m.docsNote)}</span></div>` : '');
  }

  p.status.innerHTML = m.status ? `<div class="meta-status"><span class="spinner"></span><span class="shimmer">${esc(m.status)}</span></div>`
    : (streaming && !content && !thinking ? `<div class="meta-status"><span class="spinner"></span>${m.provider && !isLocal(m.provider) ? `<span class="shimmer">Waiting for ${esc(provName(m.provider))}…</span>` : ''}</div>` : '');

  if (thinking) {
    if (m._thinkOpen === undefined) m._thinkOpen = streaming && S.settings.showThinking;
    if (streaming && !isThinking && m._autoCollapsed !== true && content) { m._thinkOpen = false; m._autoCollapsed = true; }
    const label = isThinking ? '<span class="shimmer">Thinking…</span>' : (m.thinkMs ? `Thought for ${Math.max(1, Math.round(m.thinkMs / 1000))}s` : 'Thoughts');
    if (!p.think.firstChild) p.think.innerHTML = `<div class="think"><button class="think-head" data-act="think"><span class="chev">${ic('chevr')}</span><span class="lbl"></span></button><div class="think-body"></div></div>`;
    const t = p.think.firstChild;
    t.classList.toggle('open', !!m._thinkOpen);
    $('.lbl', t).innerHTML = label;
    const body = $('.think-body', t);
    const stick = body.scrollHeight - body.scrollTop - body.clientHeight < 30;
    body.textContent = thinking;
    if (isThinking && stick) body.scrollTop = body.scrollHeight;
  } else p.think.innerHTML = '';

  // Markdown parse + syntax highlighting is by far the most expensive work
  // here, and it used to run on every animation frame for the whole message,
  // which is O(n²) over a reply. Skip it when nothing changed, and while
  // streaming cap it to ~11 fps; the final frame always renders.
  const now = performance.now();
  const srcN = m.sources?.length || 0;
  if ((content !== p._mdContent || srcN !== p._mdSrcN) && (!streaming || now - (p._mdAt || 0) >= 90)) {
    p._mdContent = content; p._mdSrcN = srcN; p._mdAt = now;
    p.prose.innerHTML = renderMD(content);
    linkCitations(p.prose, m.sources);
  }
  // When the re-render above is skipped, the cursor from an earlier frame is
  // still there: drop it, so a frame never shows two and a finished reply none.
  p.prose.querySelector('.cursor')?.remove();
  if (streaming && content && !isThinking) {
    let last = p.prose.lastElementChild;
    while (last?.lastElementChild && /^(UL|OL|LI|BLOCKQUOTE)$/.test(last.tagName)) last = last.lastElementChild;
    if (last && /^(P|LI|H[1-6])$/.test(last.tagName)) last.insertAdjacentHTML('beforeend', '<span class="cursor"></span>');
  }

  const tk = (m.toolCalls || []).map(c => c.status + (c.result?.length || 0) + (S.mcp.waiters.has(m.id + c.id) ? 'w' : '')).join(',');
  if (p.tools.dataset.k !== tk) { p.tools.dataset.k = tk; p.tools.innerHTML = (m.toolCalls || []).map(c => toolCard(m, c)).join(''); }

  p.err.innerHTML = m.error ? `<div class="err-box">${ic('alert')}<span>${esc(m.error)}</span></div>` : '';

  p.actions.classList.toggle('hidden', streaming);
  if (!streaming) p.stats.textContent = statsLine(m);
  if (!host) p.fork.innerHTML = forkNav(m);
  if (host) {
    const picked = host.cols[host.pick] === m;
    p.head.innerHTML = `<span class="cmp-model">${esc(m.model || '')}</span><span class="cmp-prov">${esc(provName(m.provider))}${isLocal(m.provider) ? '' : ' · cloud'}</span>${picked ? `<span class="cmp-badge">${ic('check')}Using this</span>` : ''}`;
    el.classList.toggle('picked', picked);
    const canPick = !streaming && S.chat?.messages.at(-1) === host && !S.streams.has(S.chat.id);
    p.pick.classList.toggle('hidden', !canPick || picked);
    p.pick.textContent = 'Use this answer';
  }
}

let renderQueued = new Set();
export function queueRender(chat, m) {
  if (S.chat !== chat) return;
  if (renderQueued.has(m.id)) return;
  renderQueued.add(m.id);
  requestAnimationFrame(() => {
    renderQueued.delete(m.id);
    if (S.chat !== chat) return;
    const el = $(`#messages [data-id="${m.id}"]`);
    if (el) { const near = isNearBottom(); renderAssistant(el, m, el._host); if (near) scrollToBottom(); }
  });
}
