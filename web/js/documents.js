// Local document search (RAG) UI
import { FOLDER_KEEP_DOT, FOLDER_SECRET, FOLDER_SKIP, TEXT_EXT } from './composer.js';
import { openModels, pullModel } from './ollama-models.js';
import { S, setSetting } from './state.js';
import { $, api, esc, fmtBytes, ic, toast } from './util.js';

// ───────────────────────── documents (local search) ─────────────────────────
export async function loadRag() {
  try { S.rag.docs = (await api('/rag')).docs || []; } catch { S.rag.docs = []; }
  $('#ragToggle').classList.toggle('on', S.settings.ragOn && S.rag.docs.some(d => d.enabled));
}
function renderRag() {
  const provs = S.providers.filter(p => p.enabled && p.local);
  $('#ragProvSel').innerHTML = provs.map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join('');
  if (!provs.some(p => p.id === S.settings.ragProvider) && provs[0]) setSetting('ragProvider', provs[0].id);
  $('#ragProvSel').value = S.settings.ragProvider;
  $('#ragModelIn').value = S.settings.ragModel;
  const re = /embed|bge|minilm|e5|gte|arctic|nomic/i;
  $('#ragModelList').innerHTML = S.models.filter(m => m.provider === S.settings.ragProvider && re.test(m.id)).map(m => `<option value="${esc(m.id)}">`).join('');
  const L = S.rag.docs;
  $('#ragList').innerHTML = L.length ? L.map(d => `<div class="pl-row" data-id="${esc(d.id)}"><div class="pl-txt"><b>${esc(d.name)}</b><span>${d.chunks} passages · ${fmtBytes(d.bytes)} · ${esc(d.model)}</span></div>
    <input type="checkbox" class="switch" data-rag="on" title="Use in answers" ${d.enabled ? 'checked' : ''}><button class="icon-btn sm" data-rag="del" title="Remove">${ic('trash')}</button></div>`).join('')
    : '<div class="muted small">No documents yet. Add some text files or a folder.</div>';
  const busy = !!S.rag.busy;
  $('#ragCancel').classList.toggle('hidden', !busy);
  $('#ragAddFiles').disabled = $('#ragAddFolder').disabled = busy;
}
function ragMsg(t, err) { const m = $('#ragMsg'); m.textContent = t || ''; m.classList.toggle('err', !!err); }
async function openRag() { $('#ragModal').classList.remove('hidden'); ragMsg(''); $('#ragPull').classList.add('hidden'); await loadRag(); renderRag(); }
function ragNeedsPull(msg) {
  const model = S.settings.ragModel;
  if (S.settings.ragProvider !== 'ollama' || !/not found|pull/i.test(msg)) return;
  const b = $('#ragPull'); b.textContent = `Pull ${model}`; b.dataset.model = model; b.classList.remove('hidden');
}
async function indexItems(items) {
  if (!items.length || S.rag.busy) return;
  if (!S.settings.ragModel.trim()) { toast('Enter an embedding model first', true); return; }
  const ctl = new AbortController(); S.rag.busy = ctl; renderRag();
  let ok = 0, failed = 0;
  $('#ragPull').classList.add('hidden');
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    ragMsg(`Indexing ${it.name} (${i + 1}/${items.length})…`);
    try {
      await api('/rag/docs', { method: 'POST', signal: ctl.signal, body: JSON.stringify({ name: it.name, text: it.text, provider: S.settings.ragProvider, model: S.settings.ragModel.trim() }) });
      ok++;
    } catch (e) {
      if (e.name === 'AbortError') break;
      failed++; ragMsg(`${it.name}: ${e.message}`, true); ragNeedsPull(e.message);
      if (/embedding failed|not found|local|provider/i.test(e.message)) break; // systemic problem — don't repeat it for every file
    }
  }
  S.rag.busy = null;
  await loadRag(); renderRag();
  if (ok) ragMsg(`Indexed ${ok} file${ok === 1 ? '' : 's'}${failed ? `, ${failed} failed` : ''}.`);
}
async function ragFilesToItems(files, folder) {
  const items = []; let skipped = 0;
  for (const f of files) {
    const rel = folder ? (f.webkitRelativePath || f.name) : f.name;
    const base = rel.split('/').pop();
    if (folder && (FOLDER_SKIP.test(rel) || FOLDER_SECRET.test(rel) || (base.startsWith('.') && !FOLDER_KEEP_DOT.test(rel)))) { skipped++; continue; }
    if (FOLDER_SECRET.test(rel) || f.type.startsWith('image/') || f.type === 'application/pdf' || f.size > 5 << 20) { skipped++; continue; }
    if (!TEXT_EXT.test(f.name) && !f.type.startsWith('text/') && !/^(Dockerfile|Makefile|LICENSE|README)$/i.test(f.name)) { skipped++; continue; }
    if (items.length >= 400) { skipped++; continue; }
    let text; try { text = await f.text(); } catch { skipped++; continue; }
    if (/\u0000/.test(text.slice(0, 8000)) || !text.trim()) { skipped++; continue; }
    items.push({ name: folder ? rel : f.name, text });
  }
  if (skipped) toast(`${skipped} file${skipped === 1 ? '' : 's'} skipped (not plain text, too large, or secrets)`);
  return items;
}
export function bindRag() {
  $('#ragBtn').onclick = openRag;
  $('#ragToggle').onclick = () => {
    if (!S.settings.ragOn && !S.rag.docs.some(d => d.enabled)) { toast('Add some documents first'); openRag(); return; }
    setSetting('ragOn', !S.settings.ragOn);
    $('#ragToggle').classList.toggle('on', S.settings.ragOn);
  };
  $('#ragProvSel').onchange = e => { setSetting('ragProvider', e.target.value); renderRag(); };
  $('#ragModelIn').oninput = e => setSetting('ragModel', e.target.value);
  $('#ragAddFiles').onclick = () => $('#ragFileIn').click();
  $('#ragAddFolder').onclick = () => $('#ragFolderIn').click();
  $('#ragFileIn').onchange = async e => { const fl = [...e.target.files]; e.target.value = ''; indexItems(await ragFilesToItems(fl, false)); };
  $('#ragFolderIn').onchange = async e => { const fl = [...e.target.files]; e.target.value = ''; indexItems(await ragFilesToItems(fl, true)); };
  $('#ragCancel').onclick = () => S.rag.busy?.abort();
  $('#ragPull').onclick = async e => {
    const model = e.target.dataset.model; $('#ragModal').classList.add('hidden');
    await openModels(); $('#pullName').value = model; pullModel();
  };
  $('#ragTest').onclick = async () => {
    ragMsg('Testing…'); $('#ragPull').classList.add('hidden');
    try { const r = await api('/rag/test', { method: 'POST', body: JSON.stringify({ provider: S.settings.ragProvider, model: S.settings.ragModel.trim() }) }); ragMsg(`Works — ${r.dim}-dimensional embeddings.`); }
    catch (e) { ragMsg(e.message, true); ragNeedsPull(e.message); }
  };
  $('#ragList').onclick = async e => {
    const b = e.target.closest('[data-rag]'); if (!b) return;
    const id = b.closest('.pl-row').dataset.id;
    try {
      if (b.dataset.rag === 'del') { await api('/rag/docs/' + id, { method: 'DELETE' }); }
      else await api('/rag/docs/' + id, { method: 'PUT', body: JSON.stringify({ enabled: b.checked }) });
      await loadRag(); renderRag();
    } catch (err) { toast(err.message, true); }
  };
}
