// Unified model catalog and model picker
import { updateCmpUI } from './events.js';
import { isLocal, provById, provName, provParams } from './providers.js';
import { PARAM_LABELS, S, saveSettings } from './state.js';
import { updateToolsUI } from './tools.js';
import { $, $$, api, esc, fmtBytes, fmtCtx, ic, toast } from './util.js';

// ───────────────────────── models (unified catalog) ─────────────────────────
export const curProv = () => S.settings.provider || 'ollama';
export const currentModel = () => S.settings.model;
export const capKey = (p, m) => (p || 'ollama') + '|' + m;
export const findModel = (p, m) => S.models.find(x => x.provider === (p || 'ollama') && x.id === m);

export async function loadCatalog(fresh = false) {
  try {
    const r = await api('/catalog' + (fresh ? '?fresh=1' : ''));
    S.models = r.models || []; S.catalogErrors = r.errors || {};
  } catch (e) { S.models = []; S.catalogErrors = { ollama: e.message }; }
  // Local-first: if the saved choice is gone, only ever auto-pick a LOCAL model.
  if (!findModel(curProv(), currentModel())) {
    const chatSel = S.chat && findModel(S.chat.provider, S.chat.model);
    const ll = S.settings.lastLocal;
    const local = (ll && findModel(ll.provider, ll.model)) || S.models.find(m => m.local);
    if (chatSel) { S.settings.provider = S.chat.provider || 'ollama'; S.settings.model = S.chat.model; saveSettings(); }
    else if (local) {
      const was = currentModel() && `${currentModel()} (${provName(curProv())})`;
      S.settings.provider = local.provider; S.settings.model = local.id; saveSettings();
      if (was) toast(`${was} isn't available — switched to local ${local.id}`);
    }
    else if (isLocal(curProv())) { S.settings.model = ''; }
    // an explicitly chosen cloud model whose list failed to load is kept as-is;
    // the request will fail visibly instead of being rerouted.
  }
  renderModelList(); updateModelUI();
  scheduleCatalogRetry();
}

// A provider that comes up a moment after the UI (guanaco-server.bat launches
// both at once) would otherwise sit in the error state until a manual
// refresh. Retry a few times with backoff while nothing loaded.
let catalogRetries = 0, catalogTimer = null;
function scheduleCatalogRetry() {
  if (S.models.length) { catalogRetries = 0; return; }
  if (catalogTimer || catalogRetries >= 3) return;
  if (!Object.keys(S.catalogErrors).some(id => provById(id)?.enabled)) return;
  const delay = 4000 * 2 ** catalogRetries++;
  catalogTimer = setTimeout(() => { catalogTimer = null; loadCatalog(false); }, delay);
}

export async function getCaps(provider, model) {
  if (!model) return null;
  const k = capKey(provider, model);
  if (S.caps[k]) return S.caps[k];
  const entry = findModel(provider, model);
  if ((provider || 'ollama') === 'ollama') {
    try {
      const info = await api('/show', { method: 'POST', body: JSON.stringify({ model }) });
      const mi = info.model_info || {};
      const ctxKey = Object.keys(mi).find(x => x.endsWith('.context_length'));
      S.caps[k] = { capabilities: info.capabilities || ['completion'], details: info.details || {}, ctx: ctxKey ? mi[ctxKey] : null };
    } catch { S.caps[k] = { capabilities: ['completion'], details: {}, ctx: null }; }
  } else {
    const caps = ['completion'];
    if (entry?.vision !== false) caps.push('vision');   // unknown → allow
    if (entry?.thinking) caps.push('thinking');
    S.caps[k] = { capabilities: caps, details: {}, ctx: entry?.context || null };
  }
  return S.caps[k];
}
export const hasCap = (p, m, c) => S.caps[capKey(p, m)]?.capabilities?.includes(c);

export async function updateModelUI() {
  const prov = curProv(), model = currentModel(), local = isLocal(prov);
  const entry = findModel(prov, model);
  $('#modelName').textContent = model ? (entry?.name || model) : (S.models.length ? 'Select model' : 'No models');
  $('#modelProvIc').innerHTML = ic(local ? 'chip' : 'cloud');
  $('#modelProvIc').className = 'prov-ic ' + (local ? 'local' : 'cloud');
  $('#modelCloud').textContent = provName(prov);
  $('#modelCloud').classList.toggle('hidden', local || !model);
  $('#composer').classList.toggle('cloud', !local && !!model);
  $('#disclaimer').innerHTML = !local && model
    ? `${ic('cloud')} Messages in this chat are sent to <b>${esc(provName(prov))}</b>. Ollama models never leave your PC.`
    : 'Models can make mistakes. Double-check important answers.';
  applyParamVisibility();

  const caps = await getCaps(prov, model);
  if (prov !== curProv() || model !== currentModel()) return; // selection changed meanwhile
  const box = $('#modelInfo');
  if (!model || !caps) { box.innerHTML = '<span class="muted">No model selected</span>'; }
  else {
    const d = caps.details;
    box.innerHTML = `<div class="name">${esc(entry?.name || model)}</div>
      <div class="chips"><span class="chip ${local ? 'loc' : 'cld'}">${ic(local ? 'chip' : 'cloud')}${esc(provName(prov))}</span>${caps.capabilities.filter(c => c !== 'completion').map(c => `<span class="chip acc">${esc(c)}</span>`).join('')}</div>
      <div class="kv">
        ${entry?.name && entry.name !== model ? `<span>ID</span><b>${esc(model)}</b>` : ''}
        ${d.parameter_size ? `<span>Parameters</span><b>${esc(d.parameter_size)}</b>` : ''}
        ${d.quantization_level ? `<span>Quantization</span><b>${esc(d.quantization_level)}</b>` : ''}
        ${d.family ? `<span>Family</span><b>${esc(d.family)}</b>` : ''}
        ${entry?.size ? `<span>Size</span><b>${fmtBytes(entry.size)}</b>` : ''}
        ${caps.ctx ? `<span>Max context</span><b>${fmtCtx(caps.ctx)}</b>` : ''}
      </div>`;
  }
  const think = hasCap(prov, model, 'thinking');
  $('#thinkToggle').classList.toggle('hidden', !think);
  $('#thinkToggle').classList.toggle('on', think && S.settings.thinkMode === 'on');
  updateCmpUI(); updateToolsUI();
}

// Hide generation controls the current provider doesn't support.
function applyParamVisibility() {
  const allowed = provParams(curProv());
  const hidden = [];
  $$('#pane-settings [data-param]').forEach(el => {
    const ok = allowed.includes(el.dataset.param);
    el.classList.toggle('hidden', !ok);
    if (!ok) hidden.push(PARAM_LABELS[el.dataset.param]);
  });
  const note = $('#paramNote');
  note.classList.toggle('hidden', !hidden.length);
  note.textContent = hidden.length ? `${provName(curProv())} doesn't support ${hidden.join(', ')} — those stay Ollama-only and aren't sent.` : '';
  $('#paramProv').textContent = provName(curProv());
}

export function renderModelList() {
  const q = S.modelQuery.trim().toLowerCase();
  const list = $('#modelList');
  const enabled = S.providers.filter(p => p.enabled);
  if (!enabled.length && !S.models.length) { list.innerHTML = `<div class="mm-empty">Can't reach the Guanaco server.</div>`; return; }
  let html = S.menuFor === 'cmp' ? `<div class="mm-hint">Compare ${esc(currentModel() || 'the selected model')} with…</div>` : '';
  for (const p of enabled) {
    const ms = S.models.filter(m => m.provider === p.id && (!q || m.id.toLowerCase().includes(q) || (m.name || '').toLowerCase().includes(q)));
    const err = S.catalogErrors[p.id];
    if (q && !ms.length) continue;
    html += `<div class="mm-group"><span class="mm-gh ${p.local ? 'loc' : 'cld'}">${ic(p.local ? 'chip' : 'cloud')}${esc(p.name)}<em>${p.local ? 'local' : 'cloud'}</em></span>`;
    if (err) html += `<div class="mm-err">${ic('alert')}<span>${esc(err)}</span></div>`;
    else if (!ms.length) html += `<div class="mm-empty">${p.kind === 'ollama' ? 'No models yet — pull one from Ollama models.' : 'No models'}</div>`;
    for (const m of ms) {
      const sel = S.menuFor === 'cmp' ? (m.provider === S.cmp.provider && m.id === S.cmp.model) : (m.provider === curProv() && m.id === currentModel());
      const d = m.details || {};
      const sub = m.local && p.kind === 'ollama' ? [d.parameter_size, d.quantization_level, fmtBytes(m.size)].filter(Boolean).join(' · ')
        : [m.name !== m.id ? m.id : '', m.thinking ? 'thinking' : '', m.context ? fmtCtx(m.context) + ' ctx' : ''].filter(Boolean).join(' · ');
      html += `<button type="button" class="mm-item${sel ? ' sel' : ''}" data-prov="${esc(m.provider)}" data-model="${esc(m.id)}">
        <div class="mm-top"><span class="mm-name">${esc(m.name || m.id)}</span>${sel ? `<span class="check-ic">${ic('check')}</span>` : ''}</div>
        ${sub ? `<span class="mm-sub">${esc(sub)}</span>` : ''}</button>`;
    }
    html += '</div>';
  }
  if (!S.providers.some(p => !p.local && p.enabled) && !q) html += `<button type="button" class="mm-add" data-providers>${ic('plus')}Add OpenAI, Anthropic, Gemini or a custom endpoint…</button>`;
  list.innerHTML = html || `<div class="mm-empty">No models match “${esc(q)}”</div>`;
}

export function selectModel(provider, model) {
  const wasLocal = isLocal(curProv()), nowLocal = isLocal(provider);
  S.settings.provider = provider; S.settings.model = model;
  if (nowLocal) S.settings.lastLocal = { provider, model };
  saveSettings();
  if (S.chat) { S.chat.provider = provider; S.chat.model = model; }
  if (wasLocal && !nowLocal) toast(`Switched to ${provName(provider)} — messages will be sent to the cloud`);
  renderModelList(); updateModelUI();
}
