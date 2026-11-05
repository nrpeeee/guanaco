// Ollama models dialog (pull / delete)
import { loadCatalog } from './models.js';
import { $, api, esc, fmtBytes, ic, toast } from './util.js';

// ───────────────────────── Ollama models modal ─────────────────────────
const POPULAR_MODELS = ['llama3.2:3b', 'qwen2.5-coder:7b', 'gemma3:4b', 'mistral:7b', 'phi4-mini', 'deepseek-r1:8b'];
export let pullCtl = null, modelsTimer = null, modelTags = [];

export async function openModels() {
  $('#modelsModal').classList.remove('hidden');
  $('#pullChips').innerHTML = POPULAR_MODELS.map(n => `<button type="button" class="pull-chip" data-pull="${esc(n)}">${esc(n)}</button>`).join('');
  await refreshModelsModal();
  $('#pullName').focus();
  clearInterval(modelsTimer);
  // keep "loaded in memory" fresh while the dialog is open
  modelsTimer = setInterval(() => { if ($('#modelsModal').classList.contains('hidden')) clearInterval(modelsTimer); else refreshModelsModal(); }, 5000);
}

export async function refreshModelsModal() {
  let ps = [], ok = true;
  try { modelTags = (await api('/models')).models || []; } catch { modelTags = []; ok = false; }
  try { ps = (await api('/ps')).models || []; } catch { }
  $('#modelsOffline').classList.toggle('hidden', ok);
  $('#psList').innerHTML = ps.map(m => `<div class="mt-row ps"><span class="nm" title="${esc(m.name)}">${esc(m.name)}</span><span class="c">${fmtBytes(m.size_vram)} VRAM</span>
    <span class="c hide-sm">${m.size_vram < m.size ? Math.round(100 * m.size_vram / m.size) + '% GPU' : '100% GPU'}</span><button class="icon-btn sm" data-unload="${esc(m.name)}" title="Unload from memory">${ic('x')}</button></div>`).join('');
  renderModelTable();
}

export function renderModelTable() {
  const q = $('#modelFilter').value.trim().toLowerCase(), sort = $('#modelSort').value;
  const total = modelTags.reduce((n, m) => n + (m.size || 0), 0);
  $('#modelTotal').textContent = modelTags.length ? `· ${modelTags.length} models, ${fmtBytes(total)} on disk` : '';
  const list = modelTags.filter(m => !q || m.name.toLowerCase().includes(q) || (m.details?.family || '').toLowerCase().includes(q));
  list.sort(sort === 'size' ? (a, b) => (b.size || 0) - (a.size || 0)
    : sort === 'recent' ? (a, b) => String(b.modified_at || '').localeCompare(String(a.modified_at || ''))
      : (a, b) => a.name.localeCompare(b.name));
  $('#modelTable').innerHTML = list.map(m => {
    const when = m.modified_at ? new Date(m.modified_at).toLocaleDateString() : '';
    return `<div class="mt-row"><span class="nm" title="${esc(m.name)}${when ? ' · updated ' + esc(when) : ''}">${esc(m.name)}</span>
    <span class="c hide-sm">${esc(m.details?.parameter_size || '')}</span><span class="c hide-sm">${esc(m.details?.quantization_level || '')}</span>
    <span class="c">${fmtBytes(m.size)}</span><button class="icon-btn sm" data-delmodel="${esc(m.name)}" title="Delete">${ic('trash')}</button></div>`;
  }).join('');
}

async function pullOne(name, signal, bar, txt) {
  const r = await fetch('/api/pull', { method: 'POST', signal, headers: { 'X-Guanaco': '1', 'Content-Type': 'application/json' }, body: JSON.stringify({ model: name }) });
  const reader = r.body.getReader(), dec = new TextDecoder(); let buf = '', err = null;
  for (; ;) {
    const { value, done } = await reader.read(); if (done) break;
    buf += dec.decode(value, { stream: true });
    let nl;
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl); buf = buf.slice(nl + 1);
      let ev; try { ev = JSON.parse(line); } catch { continue; }
      if (ev.error) { err = ev.error; continue; }
      if (ev.total) { const pct = (ev.completed || 0) / ev.total * 100; bar.style.width = pct + '%'; txt.textContent = `${name}: ${ev.status} — ${fmtBytes(ev.completed || 0)} / ${fmtBytes(ev.total)} (${pct.toFixed(0)}%)`; }
      else txt.textContent = `${name}: ${ev.status || ''}`;
    }
  }
  if (err) throw new Error(err);
}

export async function pullModel() {
  const names = $('#pullName').value.split(/[,\s]+/).map(s => s.trim()).filter(Boolean);
  if (!names.length) return;
  const box = $('#pullProgress'), bar = $('.bar i', box), txt = $('.txt', box);
  box.classList.remove('hidden'); bar.style.width = '0'; txt.textContent = 'Starting…';
  $('#pullBtn').disabled = true; $('#pullCancel').classList.remove('hidden');
  pullCtl = new AbortController();
  try {
    for (const name of names) {
      bar.style.width = '0';
      await pullOne(name, pullCtl.signal, bar, txt);
      toast(`Pulled ${name}`);
      await refreshModelsModal(); loadCatalog();
    }
    txt.textContent = names.length > 1 ? `Pulled ${names.length} models` : `Pulled ${names[0]}`; bar.style.width = '100%';
    $('#pullName').value = '';
  } catch (e) {
    if (e.name === 'AbortError') txt.textContent = 'Cancelled (partial downloads resume next time)';
    else { txt.textContent = 'Error: ' + e.message; toast(e.message, true); }
  } finally { $('#pullBtn').disabled = false; $('#pullCancel').classList.add('hidden'); pullCtl = null; }
}
