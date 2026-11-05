// Sending and streaming a reply
import { beginFork } from './branches.js';
import { saveChat, upsertMeta } from './chats.js';
import { queueRender, refreshMsg, renderAssistant, renderUser, syncPick } from './compare.js';
import { autosize, renderPending, updateSendBtn } from './composer.js';
import { openModelMenu } from './events.js';
import { splitThink } from './markdown.js';
import { capKey, curProv, currentModel, findModel, getCaps, hasCap } from './models.js';
import { isLocal, provParams } from './providers.js';
import { renderAll, renderTopbar, scrollToBottom } from './render.js';
import { S } from './state.js';
import { presetById, sysTarget } from './system-prompt.js';
import { denyPending, runToolCalls, toolsActive } from './tools.js';
import { $, api, toast, uid } from './util.js';
import { speak, stopSpeak } from './voice.js';

// ───────────────────────── sending & streaming ─────────────────────────
function newChatObj() {
  const now = Date.now();
  return { id: uid(), title: 'New chat', provider: curProv(), model: currentModel(), created: now, updated: now, pinned: false, system: S.draft.system, presetId: S.draft.presetId, messages: [] };
}

// Normalized wire format: images carry their MIME type so cloud APIs get it right.
function toWire(msgs, system) {
  const out = [];
  if (system?.trim()) out.push({ role: 'system', content: system.trim() });
  for (const m of msgs) {
    if (m.role === 'user') {
      let c = m.content || '';
      const files = (m.attachments || []).filter(a => (a.kind === 'file' && a.text != null) || (a.kind === 'folder' && a.files?.length));
      if (files.length) {
        const block = files.map(f => (f.kind === 'folder'
          ? f.files.map(x => `<file name="${(f.name + '/' + x.path).replace(/"/g, "'")}">\n${x.text}\n</file>`).join('\n\n')
          : `<file name="${String(f.name).replace(/"/g, "'")}">\n${f.text}\n</file>`)).join('\n\n');
        c = block + (c ? '\n\n' + c : '');
      }
      const o = { role: 'user', content: c };
      if (m.images?.length) {
        const mimes = (m.attachments || []).filter(a => a.kind === 'image').map(a => a.mime);
        o.images = m.images.map((data, i) => ({ mime: mimes[i] || '', data }));
      }
      out.push(o);
    } else if (m.role === 'assistant' && !m.error && m.toolCalls?.some(c => ['done', 'error', 'denied'].includes(c.status))) {
      const st = splitThink(m.content || ''), done = m.toolCalls.filter(c => ['done', 'error', 'denied'].includes(c.status));
      out.push({ role: 'assistant', content: st ? st.content : (m.content || ''), tool_calls: done.map(c => ({ id: c.id, name: c.ns, args: c.args })) });
      done.forEach(c => out.push({ role: 'tool', tool_call_id: c.id, name: c.ns, content: c.result || '' }));
    } else if (m.role === 'assistant' && !m.error && m.content) {
      const st = splitThink(m.content);
      out.push({ role: 'assistant', content: st ? st.content : m.content });
    }
  }
  return out;
}

// Only params the provider supports are sent (the server filters again).
function buildParams(provider, model) {
  const p = S.settings.params, allowed = provParams(provider), o = {};
  const num = v => v !== '' && v != null && !isNaN(v);
  if (allowed.includes('temperature') && num(p.temperature)) o.temperature = Number(p.temperature);
  const prof = presetById(sysTarget().presetId);
  if (allowed.includes('temperature') && prof?.temperature != null) o.temperature = Number(prof.temperature);
  if (allowed.includes('top_p') && num(p.top_p)) o.top_p = Number(p.top_p);
  if (allowed.includes('top_k') && num(p.top_k)) o.top_k = Number(p.top_k);
  if (allowed.includes('repeat_penalty') && num(p.repeat_penalty)) o.repeat_penalty = Number(p.repeat_penalty);
  if (allowed.includes('num_ctx')) {
    // 0 = "Model default": Ollama itself would fall back to 4K, so send the model's real context length.
    const nc = Number(p.num_ctx) > 0 ? Number(p.num_ctx) : Number(S.caps[capKey(provider, model)]?.ctx) || 0;
    if (nc > 0) o.num_ctx = nc;
  }
  if (allowed.includes('max_tokens') && num(p.num_predict) && Number(p.num_predict) !== 0) o.max_tokens = Number(p.num_predict);
  if (allowed.includes('seed') && num(p.seed)) o.seed = Number(p.seed);
  return o;
}

export async function send() {
  const text = $('#input').value.trim();
  if (!text && !S.pending.length) return;
  if (!currentModel()) { toast('Pick a model first — pull one from Ollama models or add a provider', true); return; }
  if (S.cmp.on) {
    if (!S.cmp.model) { toast('Pick the second model to compare with', true); openModelMenu(true); return; }
    if (!findModel(S.cmp.provider, S.cmp.model)) { toast('The comparison model is no longer available — pick another', true); openModelMenu(true); return; }
    if (S.cmp.provider === curProv() && S.cmp.model === currentModel()) { toast('Pick two different models to compare', true); openModelMenu(true); return; }
  }
  if (!S.chat) { S.chat = newChatObj(); S.cache.set(S.chat.id, S.chat); history.replaceState(null, '', '#/c/' + S.chat.id); }
  const chat = S.chat;
  if (S.streams.has(chat.id)) return;
  stopSpeak();
  const atts = S.pending;
  const msg = {
    id: uid(), role: 'user', content: text, created: Date.now(),
    attachments: atts.map(a => a.kind === 'image'
      ? { kind: 'image', name: a.name, size: a.size, mime: a.mime }
      : a.kind === 'folder'
        ? { kind: 'folder', name: a.name, size: a.size, count: a.count, files: a.files }
        : { kind: 'file', name: a.name, size: a.size, text: a.text }),
    images: atts.filter(a => a.kind === 'image').map(a => a.data),
  };
  if (!msg.images.length) delete msg.images;
  if (!msg.attachments.length) delete msg.attachments;
  chat.messages.push(msg);
  $('#input').value = ''; S.pending = []; renderPending(); autosize();
  renderAll(); scrollToBottom(true);
  generate(chat);
}

// Stream one assistant reply (or one compare column) into `am`.
async function streamInto(chat, am, history, ctrl, host0) {
  const { provider, model } = am, local = isLocal(provider);
  delete am.status;
  let thinkStart = 0;
  try {
    await getCaps(provider, model);
    const body = { provider, model, messages: toWire(history, chat.system), params: buildParams(provider, model) };
    if (provParams(provider).includes('keep_alive') && S.settings.keepAlive) body.keep_alive = S.settings.keepAlive;
    if (hasCap(provider, model, 'thinking') && S.settings.thinkMode !== 'auto') body.think = S.settings.thinkMode === 'on';
    if (S.settings.searchOn) body.search = {
      enabled: true, results: Number(S.settings.searchResults), engine: S.settings.searchEngine, searx_url: S.settings.searxUrl,
      rewrite: S.settings.searchRewrite && (local || S.settings.cloudHelpers),
    };
    if (toolsActive() && !host0) body.tools = true;
    if (S.settings.ragOn && S.rag.docs.some(d => d.enabled)) body.rag = { enabled: true, k: Number(S.settings.ragK) || 5, allow_cloud: !!S.settings.ragCloud };
    const r = await fetch('/api/chat', { method: 'POST', headers: { 'X-Guanaco': '1', 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: ctrl.signal });
    if (!r.ok) throw new Error((await r.text()) || r.statusText);
    const reader = r.body.getReader(), dec = new TextDecoder();
    let buf = '';
    for (; ;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let nl;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim(); buf = buf.slice(nl + 1);
        if (!line) continue;
        let ev; try { ev = JSON.parse(line); } catch { continue; }
        switch (ev.t) {
          case 'status': am.status = ev.c; break;
          case 'sources': am.sources = ev.sources; am.searchQuery = ev.query; break;
          case 'tool_calls': am.toolCalls = (am.toolCalls || []).concat(ev.calls.map(c => ({ id: c.id, ns: c.ns, server: c.server, tool: c.tool || c.ns, args: c.args || {}, readOnly: !!c.read_only, known: !!c.known, status: 'pending' }))); break;
          case 'docs': am.docs = (ev.docs || []).map(d => ({ doc: d.doc, idx: d.idx, score: d.score, text: (d.text || '').slice(0, 700) })); am.docsNote = ev.skipped; if (!am.docs.length) delete am.docs; break;
          case 'delta':
            if (ev.th) { if (!thinkStart) thinkStart = Date.now(); am.thinking += ev.th; }
            if (ev.c) {
              if (thinkStart && !am.thinkMs) am.thinkMs = Date.now() - thinkStart;
              if (!thinkStart && /^\s*<think>/.test(am.content + ev.c)) thinkStart = Date.now();
              am.content += ev.c;
              if (thinkStart && !am.thinkMs && /<\/think>/.test(am.content)) am.thinkMs = Date.now() - thinkStart;
            }
            break;
          case 'done': am.stats = ev.stats; break;
          case 'error': am.error = ev.c; break;
        }
        queueRender(chat, am);
      }
    }
  } catch (e) {
    if (e.name === 'AbortError') am.stopped = true;
    else am.error = e.message || String(e);
  } finally {
    delete am.status;
    const st = splitThink(am.content);
    if (st) { am.thinking = (am.thinking ? am.thinking + '\n' : '') + st.thinking; am.content = st.content; }
    if (thinkStart && !am.thinkMs) am.thinkMs = Date.now() - thinkStart;
    if (!am.thinking) delete am.thinking;
    delete am._streaming;
    if (S.chat === chat) { const el = $(`#messages [data-id="${am.id}"]`); if (el) renderAssistant(el, am, el._host); }
  }
}

// opts.fork: fork record to attach to the new reply; opts.cmp: [{provider, model}, …] to run side by side.
async function generate(chat, opts = {}) {
  // Always the provider + model the user has selected right now. No fallbacks.
  const provider = curProv(), model = currentModel();
  chat.provider = provider; chat.model = model;
  const history = chat.messages.slice();
  const specs = opts.cmp || (S.cmp.on && S.cmp.model ? [{ provider, model }, { provider: S.cmp.provider, model: S.cmp.model }] : null);
  const mk = (p, mo) => ({ id: uid(), role: 'assistant', content: '', thinking: '', provider: p, model: mo, created: Date.now(), _streaming: true });
  let am, cols = null;
  if (specs) {
    cols = specs.map(sp => mk(sp.provider, sp.model));
    am = { id: uid(), role: 'assistant', content: '', provider: cols[0].provider, model: cols[0].model, created: Date.now(), cols, pick: 0 };
  } else am = mk(provider, model);
  if (opts.fork) am.fork = opts.fork;
  const targets = cols || [am];
  const ctrls = targets.map(() => new AbortController());
  let stopped = false;
  // Two local models run one after the other (they would fight over VRAM / swap constantly); anything involving a cloud model runs in parallel.
  const sequential = !!cols && cols.every(c => isLocal(c.provider));
  if (sequential) cols[1].status = `Waiting for ${cols[0].model} to finish…`;
  chat.messages.push(am);
  S.streams.set(chat.id, { abort() { stopped = true; ctrls.forEach(c => c.abort()); denyPending(); } });
  if (S.chat === chat) { renderAll(); scrollToBottom(true); }
  saveChat(chat);

  const go = (t, i) => {
    if (stopped) { t.stopped = true; delete t.status; delete t._streaming; return; }
    return streamInto(chat, t, history, ctrls[i], !!cols);
  };
  let again = false;
  try {
    if (sequential) for (let i = 0; i < targets.length; i++) await go(targets[i], i);
    else await Promise.all(targets.map(go));
    if (!cols && am.toolCalls?.some(c => c.status === 'pending') && !stopped) { await runToolCalls(chat, am, () => stopped); again = !stopped && (opts.depth || 0) < 8; }
  } finally {
    S.streams.delete(chat.id);
    if (cols) syncPick(am);
    if (S.chat === chat) { refreshMsg(am); updateSendBtn(); }
    if (S.settings.ttsAuto && S.chat === chat && !stopped && am.content && !am.error) speak(am.id, am.content);
    await saveChat(chat);
    if (targets.some(t => !t.error) && chat.title === 'New chat') autoTitle(chat);
  }
  if (again) generate(chat, { depth: (opts.depth || 0) + 1 });
}

async function autoTitle(chat) {
  const first = chat.messages.find(m => m.role === 'user');
  if (!first) return;
  const fallback = (first.content || first.attachments?.[0]?.name || 'New chat').split(/\s+/).slice(0, 7).join(' ').slice(0, 60);
  chat.title = fallback;
  if (S.chat === chat) renderTopbar();
  upsertMeta(chat);
  // Cloud chats only get a model-written title if the user allowed helper calls.
  const allowed = isLocal(chat.provider) || S.settings.cloudHelpers;
  if (S.settings.autoTitle && first.content && allowed) {
    try {
      const r = await api('/title', { method: 'POST', body: JSON.stringify({ provider: chat.provider, model: chat.model, text: first.content }) });
      if (r.title) chat.title = r.title;
    } catch { }
  }
  await saveChat(chat, false);
  if (S.chat === chat) renderTopbar();
}

export function stop() { if (S.chat) S.streams.get(S.chat.id)?.abort(); }

export async function regenerate(msgId) {
  const chat = S.chat; if (!chat || S.streams.has(chat.id)) return;
  const i = chat.messages.findIndex(m => m.id === msgId);
  if (i < 0) return;
  // A compare reply re-runs the same pair of models; either way the old reply is kept as another version.
  const cmp = chat.messages[i].cols ? chat.messages[i].cols.map(c => ({ provider: c.provider, model: c.model })) : null;
  if (cmp && !cmp.every(sp => findModel(sp.provider, sp.model))) { toast('One of the compared models is no longer available', true); return; }
  const fork = beginFork(chat, i);
  renderAll();
  generate(chat, { fork, cmp });
}

export function editUser(el, m) {
  const chat = S.chat; if (!chat || S.streams.has(chat.id)) return;
  const col = $('.user-col', el);
  col.innerHTML = `<div class="edit-box"><textarea></textarea><div class="row"><button class="btn" data-x>Cancel</button><button class="btn primary" data-s>Save & submit</button></div></div>`;
  const ta = $('textarea', col); ta.value = m.content;
  const fit = () => { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 'px'; };
  fit(); ta.oninput = fit; ta.focus(); ta.setSelectionRange(ta.value.length, ta.value.length);
  $('[data-x]', col).onclick = () => renderUser(el, m);
  const save = () => {
    const v = ta.value.trim(); if (!v && !m.attachments?.length) return;
    const i = chat.messages.indexOf(m);
    if (v === m.content) { const nx = chat.messages[i + 1]; if (nx) regenerate(nx.id); else renderUser(el, m); return; }
    // Edit = new version of this message; the old message and everything after it stays reachable via the ‹ n/N › switcher.
    const nm = { ...m, id: uid(), content: v, created: Date.now() };
    delete nm.fork;
    nm.fork = beginFork(chat, i);
    chat.messages.push(nm);
    renderAll(); scrollToBottom(true);
    generate(chat);
  };
  $('[data-s]', col).onclick = save;
  ta.onkeydown = e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); save(); } if (e.key === 'Escape') renderUser(el, m); };
}
