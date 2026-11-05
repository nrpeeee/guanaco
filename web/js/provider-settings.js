// Providers dialog (cloud and custom endpoints)
import { ollamaFix } from './health.js';
import { loadCatalog } from './models.js';
import { openModels } from './ollama-models.js';
import { loadProviders, provName } from './providers.js';
import { S } from './state.js';
import { $, api, ask, esc, ic, toast } from './util.js';

// ───────────────────────── Providers modal ─────────────────────────
export async function openProviders() {
  $('#providersModal').classList.remove('hidden');
  await loadProviders();
  renderProviders();
}

function provStatus(p) {
  if (p.blocked) return ['warn', 'Blocked (-lan)'];
  if (p.kind === 'ollama') return S.online ? ['ok', 'Connected'] : ['bad', 'Offline'];
  if (!p.enabled) return ['off', 'Off'];
  if (p.needs_key && !p.key_set) return ['warn', 'Needs key'];
  if (S.catalogErrors[p.id]) return ['bad', 'Error'];
  return ['ok', 'On'];
}

function renderProviders() {
  $('#provLan').classList.toggle('hidden', !S.lan);
  $('#addProv').classList.toggle('hidden', S.lan);
  $('#provProtection').innerHTML = `${ic('key')} ${esc(S.protection)} Keys are write-only: they're never sent back to the browser, and never included in exports or settings.json.`;
  $('#cloudHelpers').checked = !!S.settings.cloudHelpers;
  const ro = S.lan ? 'disabled' : '';
  $('#provList').innerHTML = S.providers.map(p => {
    const [cls, label] = provStatus(p);
    const keyRow = p.kind === 'ollama' ? '' : (p.key_set
      ? `<div class="pv-row"><span class="pv-lbl">${ic('key')}API key</span><span class="pv-key">${esc(p.key_hint)} <em>${p.key_source?.startsWith('env:') ? 'from ' + esc(p.key_source.slice(4)) : 'stored'}</em></span>
          ${p.key_source === 'stored' ? `<button class="btn sm" data-act="replace" ${ro}>Replace</button><button class="btn sm danger" data-act="unkey" ${ro}>Remove</button>` : `<button class="btn sm" data-act="replace" ${ro}>Override</button>`}</div>`
      : `<div class="pv-row"><span class="pv-lbl">${ic('key')}API key</span><input class="input" type="password" data-f="key" placeholder="${esc(keyPlaceholder(p.kind))}${p.needs_key ? '' : ' (optional)'}" autocomplete="off" ${ro}><button class="btn sm primary" data-act="savekey" ${ro}>Save</button></div>`);
    const urlRow = p.kind === 'ollama'
      ? `<div class="pv-row"><span class="pv-lbl">${ic('globe')}URL</span><code class="pv-url">${esc(p.base_url)}</code><span class="muted small">set with <code>-ollama</code></span></div>`
      : `<div class="pv-row"><span class="pv-lbl">${ic('globe')}Base URL</span><input class="input" data-f="base" value="${esc(p.base_url || '')}" placeholder="${esc(p.default_base || 'https://…/v1')}" ${ro}>${p.custom ? '' : '<span class="muted small">optional</span>'}</div>`;
    return `<div class="prov-card ${p.local ? 'loc' : 'cld'}${p.enabled ? '' : ' disabled'}" data-id="${esc(p.id)}">
      <div class="pv-head">
        <span class="pv-ic">${ic(p.kind === 'ollama' ? 'guanaco' : (p.local ? 'chip' : 'cloud'))}</span>
        <div class="pv-title">${p.custom ? `<input class="pv-name" data-f="name" value="${esc(p.name)}" ${ro}>` : `<b>${esc(p.name)}</b>`}
          <span class="pv-tags"><span class="tag ${p.local ? 'loc' : 'cld'}">${p.local ? 'Local' : 'Cloud'}</span>${p.custom ? '<span class="tag">OpenAI-compatible</span>' : ''}<span class="pill ${cls}">${label}</span></span></div>
        <div class="grow"></div>
        ${p.kind === 'ollama' ? `${S.online ? '' : '<button class="btn sm" data-act="ollamafix">Detect / start</button>'}<button class="btn sm" data-act="ollamamodels">Manage models</button>` : `<input type="checkbox" class="switch" data-act="toggle" title="Enable" ${p.enabled ? 'checked' : ''} ${ro}>`}
      </div>
      <div class="pv-body">
        ${urlRow}${keyRow}
        ${p.custom ? `<label class="check"><input type="checkbox" data-act="local" ${p.local ? 'checked' : ''} ${ro}><span>Runs on my machine / LAN</span></label>` : ''}
        <div class="pv-row end"><span class="pv-test muted small" data-test></span>
          ${p.custom ? `<button class="btn sm danger" data-act="remove" ${ro}>Remove endpoint</button>` : ''}
          <button class="btn sm" data-act="test" ${ro}>${ic('zap')}Test connection</button></div>
      </div></div>`;
  }).join('');
}
const keyPlaceholder = k => ({ openai: 'sk-…', anthropic: 'sk-ant-…', gemini: 'AIza…' }[k] || 'API key');

export async function providerAction(card, act, el) {
  const id = card.dataset.id;
  const testOut = $('[data-test]', card);
  try {
    if (act === 'toggle') await api(`/providers/${id}`, { method: 'PUT', body: JSON.stringify({ enabled: el.checked }) });
    if (act === 'local') await api(`/providers/${id}`, { method: 'PUT', body: JSON.stringify({ local: el.checked }) });
    if (act === 'savekey') {
      const key = $('[data-f=key]', card).value.trim(); if (!key) return;
      await api(`/providers/${id}/key`, { method: 'PUT', body: JSON.stringify({ key }) });
      toast('Key saved — testing…');
      await loadProviders(); renderProviders(); await loadCatalog(true);
      return providerAction($(`.prov-card[data-id="${CSS.escape(id)}"]`), 'test');
    }
    if (act === 'replace') {
      const key = await ask({ title: 'Replace API key', msg: `New key for ${provName(id)}. The old one is overwritten.`, value: '' });
      if (!key?.trim()) return;
      await api(`/providers/${id}/key`, { method: 'PUT', body: JSON.stringify({ key: key.trim() }) });
      toast('Key replaced');
    }
    if (act === 'unkey') {
      if (!await ask({ title: 'Remove API key?', msg: `${provName(id)} will stop working until you add a key again.`, input: false, okText: 'Remove', danger: true })) return;
      await api(`/providers/${id}/key`, { method: 'DELETE' });
    }
    if (act === 'remove') {
      if (!await ask({ title: 'Remove endpoint?', msg: `${provName(id)} and its stored key will be deleted. Chats are kept.`, input: false, okText: 'Remove', danger: true })) return;
      await api(`/providers/${id}`, { method: 'DELETE' });
    }
    if (act === 'test') {
      testOut.className = 'pv-test muted small'; testOut.innerHTML = '<span class="spinner"></span>Testing…';
      const r = await api(`/providers/${id}/test`, { method: 'POST' });
      testOut.className = 'pv-test small ' + (r.ok ? 'ok' : 'bad');
      testOut.textContent = r.ok ? `✓ ${r.models} models · ${r.ms} ms` : `✗ ${r.error}`;
      if (r.ok) loadCatalog(true);
      return;
    }
    if (act === 'ollamafix') { await ollamaFix(); await loadProviders(); renderProviders(); return; }
    if (act === 'ollamamodels') { $('#providersModal').classList.add('hidden'); openModels(); return; }
  } catch (e) { toast(e.message, true); }
  await loadProviders(); renderProviders(); await loadCatalog(true);
}

export async function saveProviderField(card, field, value) {
  const id = card.dataset.id;
  try {
    await api(`/providers/${id}`, { method: 'PUT', body: JSON.stringify(field === 'base' ? { base_url: value } : { name: value }) });
    await loadProviders(); renderProviders(); loadCatalog(true);
    toast('Saved');
  } catch (e) { toast(e.message, true); }
}

export async function addProvider() {
  const name = $('#newProvName').value.trim(), base_url = $('#newProvUrl').value.trim(), key = $('#newProvKey').value.trim(), local = $('#newProvLocal').checked;
  if (!base_url) { toast('Base URL is required', true); return; }
  try {
    const v = await api('/providers', { method: 'POST', body: JSON.stringify({ name, base_url, key, local }) });
    ['#newProvName', '#newProvUrl', '#newProvKey'].forEach(s => $(s).value = '');
    $('#addProv').open = false;
    await loadProviders(); renderProviders();
    await providerAction($(`.prov-card[data-id="${CSS.escape(v.id)}"]`), 'test');
  } catch (e) { toast(e.message, true); }
}
