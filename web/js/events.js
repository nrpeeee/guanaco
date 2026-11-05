// Global event wiring
import { branchChat, forkStep } from './branches.js';
import { chatMenu, closeFloating, loadChats, renameChat, renderChatList, saveChat } from './chats.js';
import { pickCol } from './compare.js';
import { addFiles, addFolders, autosize, dropAttachments, renderPending, updateSendBtn } from './composer.js';
import { download, exportChat } from './export-import.js';
import { ollamaFix } from './health.js';
import { capKey, curProv, currentModel, findModel, loadCatalog, renderModelList, selectModel, updateModelUI } from './models.js';
import { newChat, openChat, setSidebar } from './nav.js';
import { openModels, pullCtl, pullModel, refreshModelsModal, renderModelTable } from './ollama-models.js';
import { PH_RE, closeSlash, nextPlaceholder, openPrompts, openSlash, renderPromptList, savePromptForm, slashKey, usePrompt } from './prompts.js';
import { addProvider, openProviders, providerAction, saveProviderField } from './provider-settings.js';
import { isLocal, loadProviders, provById, provName } from './providers.js';
import { scroller, updateJump } from './render.js';
import { editUser, regenerate, send, stop } from './send.js';
import { applyTheme, bindSettings } from './settings-ui.js';
import { S, saveSettings, setSetting } from './state.js';
import { applyProfile, openProfile, presetById, renderSystemPanel, saveProfile, sysTarget } from './system-prompt.js';
import { resolveTool } from './tools.js';
import { $, $$, api, ask, debounce, fmtBytes, ic, toast } from './util.js';
import { speak, stopSpeak } from './voice.js';

// ───────────────────────── events ─────────────────────────
export function updateCmpUI() {
  $('#cmpToggle').classList.toggle('on', S.cmp.on);
  const b = $('#cmpModelBtn');
  b.classList.toggle('hidden', !S.cmp.on);
  $('#cmpName').textContent = S.cmp.model ? (findModel(S.cmp.provider, S.cmp.model)?.name || S.cmp.model) : 'Pick model';
  b.classList.toggle('cloud', !!S.cmp.model && !isLocal(S.cmp.provider));
}
function pickCmpModel(provider, model) {
  if (provider === curProv() && model === currentModel()) { toast('Pick a different model than the main one', true); return; }
  S.cmp.provider = provider; S.cmp.model = model;
  if (!isLocal(provider)) toast(`${provName(provider)} is a cloud provider — the compared prompt will be sent there`);
  renderModelList(); updateCmpUI();
}

export function openModelMenu(forCmp = false) {
  closeFloating(); $('#exportMenu').classList.add('hidden');
  const menu = $('#modelMenu'), want = forCmp ? 'cmp' : 'main';
  const opening = menu.classList.contains('hidden') || S.menuFor !== want;
  S.menuFor = want;
  menu.classList.toggle('hidden', !opening);
  if (opening) {
    S.modelQuery = ''; $('#modelSearch').value = ''; renderModelList();
    // open upward unless there isn't room (e.g. the centered composer on an empty chat)
    const r = $('#modelBtn').getBoundingClientRect(), above = r.top - 16, below = innerHeight - r.bottom - 16;
    const down = above < 360 && below > above;
    menu.classList.toggle('up', !down); menu.classList.toggle('down', down);
    menu.style.maxHeight = Math.min(560, Math.max(220, down ? below : above)) + 'px';
    const narrow = innerWidth <= 820;
    Object.assign(menu.style, narrow
      ? { position: 'fixed', left: '10px', right: '10px', width: 'auto', minWidth: '0', top: down ? (r.bottom + 8) + 'px' : 'auto', bottom: down ? 'auto' : (innerHeight - r.top + 8) + 'px' }
      : { position: '', left: '', right: '', width: '', minWidth: '', top: '', bottom: '' });
    setTimeout(() => $('#modelSearch').focus(), 10);
  }
}

export function bindEvents() {
  const input = $('#input');
  input.addEventListener('input', () => {
    autosize(); updateSendBtn();
    const m = /^\/([^\s]*)$/.exec(input.value);
    if (m) openSlash(m[1]); else if (S.slash.open) closeSlash();
  });
  input.addEventListener('keydown', e => {
    if (slashKey(e)) return;
    if (e.key === 'Tab' && !e.shiftKey && !e.isComposing && PH_RE.test(input.value)) { // jump between {{placeholders}}
      PH_RE.lastIndex = 0; e.preventDefault();
      if (!nextPlaceholder(input.selectionEnd)) nextPlaceholder(0);
      return;
    }
    if (e.key === 'Enter' && !e.isComposing) {
      if ((S.settings.enterSends && !e.shiftKey) || e.ctrlKey || e.metaKey) { e.preventDefault(); if (!(S.chat && S.streams.has(S.chat.id))) send(); }
    }
  });
  input.addEventListener('paste', e => {
    const files = [...(e.clipboardData?.files || [])];
    if (files.length) { e.preventDefault(); addFiles(files); }
  });
  $('#composer').onsubmit = e => { e.preventDefault(); if (S.chat && S.streams.has(S.chat.id)) stop(); else send(); };
  $('#composer').onclick = e => { if (e.target === e.currentTarget || e.target.classList.contains('composer-bar')) input.focus(); };
  $('#attachBtn').onclick = () => $('#fileInput').click();
  $('#fileInput').onchange = e => { addFiles([...e.target.files]); e.target.value = ''; };
  $('#folderBtn').onclick = () => $('#folderInput').click();
  $('#folderInput').onchange = e => {
    const fl = [...e.target.files]; e.target.value = '';
    if (fl.length) addFolders(fl.map(f => ({ file: f, rel: f.webkitRelativePath || f.name })));
  };
  $('#attachments').onclick = e => { const i = e.target.closest('[data-rm]')?.dataset.rm; if (i != null) { S.pending.splice(Number(i), 1); renderPending(); } };
  $('#searchToggle').onclick = () => { setSetting('searchOn', !S.settings.searchOn); $('#searchToggle').classList.toggle('on', S.settings.searchOn); };
  $('#thinkToggle').onclick = () => { setSetting('thinkMode', S.settings.thinkMode === 'on' ? 'off' : 'on'); bindSettings(); updateModelUI(); };
  $('#suggest').onclick = e => { const p = e.target.closest('button')?.dataset.p; if (p) { input.value = p; autosize(); input.focus(); updateSendBtn(); } };

  // model menu
  $('#modelBtn').onclick = e => { e.stopPropagation(); openModelMenu(); };
  $('#promptsBtn').onclick = e => { e.stopPropagation(); if (S.slash.open) closeSlash(); else { $('#modelMenu').classList.add('hidden'); openSlash(''); } };
  $('#slashMenu').onclick = e => {
    const it = e.target.closest('.sl-item'), act = e.target.closest('[data-sl]')?.dataset.sl;
    if (it) usePrompt(S.slash.items[Number(it.dataset.i)]);
    else if (act === 'manage') openPrompts();
    else if (act === 'save') { const t = input.value.trim(); if (!t) { toast('Type a message first, then save it as a prompt'); return; } openPrompts(t.startsWith('/') ? '' : input.value); }
  };
  $('#managePrompts').onclick = () => { $('#prefsModal').classList.add('hidden'); openPrompts(); };
  $('#plSave').onclick = savePromptForm;
  $('#plClear').onclick = () => { S.plEdit = null; $('#plName').value = ''; $('#plText').value = ''; $('#plSave').textContent = 'Add prompt'; };
  $('#promptList').onclick = e => {
    const b = e.target.closest('[data-pl]'); if (!b) return;
    const id = b.closest('.pl-row').dataset.id, p = S.settings.prompts.find(x => x.id === id); if (!p) return;
    if (b.dataset.pl === 'edit') { S.plEdit = id; $('#plName').value = p.name; $('#plText').value = p.text; $('#plSave').textContent = 'Save changes'; $('#plName').focus(); }
    else { S.settings.prompts = S.settings.prompts.filter(x => x.id !== id); if (S.plEdit === id) S.plEdit = null; saveSettings(); renderPromptList(); }
  };
  $('#cmpToggle').onclick = e => {
    e.stopPropagation();
    S.cmp.on = !S.cmp.on; updateCmpUI();
    if (S.cmp.on && !S.cmp.model) openModelMenu(true); else $('#modelMenu').classList.add('hidden');
  };
  $('#cmpModelBtn').onclick = e => { e.stopPropagation(); openModelMenu(true); };
  $('#modelSearch').oninput = e => { S.modelQuery = e.target.value; renderModelList(); };
  $('#modelSearch').onkeydown = e => {
    if (e.key === 'Enter') { e.preventDefault(); const first = $('#modelList .mm-item'); if (first) first.click(); }
    if (e.key === 'Escape') { $('#modelMenu').classList.add('hidden'); input.focus(); }
  };
  $('#modelRefresh').onclick = async e => { e.stopPropagation(); $('#modelRefresh').classList.add('spin'); await loadProviders(); await loadCatalog(true); $('#modelRefresh').classList.remove('spin'); };
  $('#modelMenu').onclick = e => {
    const b = e.target.closest('button'); if (!b || b.id === 'modelRefresh') return;
    if (b.dataset.manage != null) { $('#modelMenu').classList.add('hidden'); openModels(); }
    else if (b.dataset.providers != null) { $('#modelMenu').classList.add('hidden'); openProviders(); }
    else if (b.dataset.model != null) {
      $('#modelMenu').classList.add('hidden');
      if (S.menuFor === 'cmp') pickCmpModel(b.dataset.prov, b.dataset.model); else selectModel(b.dataset.prov, b.dataset.model);
      input.focus();
    }
  };

  $('#exportBtn').onclick = e => { e.stopPropagation(); $('#modelMenu').classList.add('hidden'); $('#exportMenu').classList.toggle('hidden'); };
  $('#exportMenu').onclick = e => { const f = e.target.closest('button')?.dataset.fmt; if (f && S.chat) { $('#exportMenu').classList.add('hidden'); exportChat(S.chat.id, f); } };
  $('#chatTitle').onclick = () => S.chat && renameChat(S.chat.id);

  document.addEventListener('click', e => {
    if (!e.target.closest('.menu-wrap')) { $('#modelMenu').classList.add('hidden'); $('#exportMenu').classList.add('hidden'); }
    if (S.slash.open && !e.target.closest('#slashMenu')) closeSlash();
    if (!e.target.closest('.menu.floating') && !e.target.closest('.more')) closeFloating();
  });

  // sidebar
  $('#newChatBtn').onclick = newChat;
  $('#collapseBtn').onclick = () => setSidebar(false);
  $('#openSidebar').onclick = () => setSidebar(true);
  $('#scrim').onclick = () => setSidebar(false);
  $$('.seg-btn').forEach(b => b.onclick = () => {
    $$('.seg-btn').forEach(x => x.classList.toggle('active', x === b));
    $('#pane-chats').classList.toggle('hidden', b.dataset.tab !== 'chats');
    $('#pane-settings').classList.toggle('hidden', b.dataset.tab !== 'settings');
  });
  $('#chatSearch').oninput = renderChatList;
  $('#chatList').onclick = e => {
    const more = e.target.closest('[data-more]');
    if (more) { e.stopPropagation(); if (more.classList.contains('open')) closeFloating(); else chatMenu(more, more.dataset.more); return; }
    const item = e.target.closest('.chat-item'); if (item) openChat(item.dataset.id);
  };

  // system prompt
  $('#presetSelect').onchange = e => {
    const t = sysTarget(), p = presetById(e.target.value);
    t.presetId = e.target.value; if (p) t.system = p.prompt;
    renderSystemPanel(); applyProfile(p); if (S.chat) saveChat(S.chat, false);
  };
  $('#systemPrompt').oninput = debounce(e => {
    const t = sysTarget(); t.system = e.target.value;
    const p = presetById(t.presetId);
    if (!p || p.prompt !== t.system) { t.presetId = 'custom'; $('#presetSelect').value = 'custom'; }
    if (S.chat) saveChat(S.chat, false);
  }, 300);
  $('#presetSave').onclick = () => openProfile(null);
  $('#presetEdit').onclick = () => openProfile($('#presetSelect').value);
  $('#pfSave').onclick = saveProfile;
  $('#pfName').onkeydown = e => { if (e.key === 'Enter') saveProfile(); };
  $('#presetDel').onclick = async () => {
    const id = $('#presetSelect').value, p = presetById(id);
    if (!p || id === 'default') return;
    if (!await ask({ title: 'Delete preset?', msg: `“${p.name}” will be removed.`, input: false, okText: 'Delete', danger: true })) return;
    S.settings.presets = S.settings.presets.filter(x => x.id !== id);
    const t = sysTarget(); t.presetId = 'custom'; saveSettings(); renderSystemPanel();
  };

  // messages (delegated)
  $('#messages').onclick = async e => {
    const cc = e.target.closest('.copy-code');
    if (cc) {
      const code = cc.closest('.code-block').querySelector('code').innerText;
      await navigator.clipboard.writeText(code);
      cc.innerHTML = ic('check') + '<span>Copied</span>'; setTimeout(() => cc.innerHTML = ic('copy') + '<span>Copy</span>', 1500);
      return;
    }
    const img = e.target.closest('.att-img');
    if (img) { lightbox(img.src); return; }
    const b = e.target.closest('[data-act]'); if (!b) return;
    const el = b.closest('.msg'), host = S.chat?.messages.find(x => x.id === el.dataset.id); if (!host) return;
    const colEl = b.closest('.cmp-col'), m = colEl ? host.cols?.find(c => c.id === colEl.dataset.id) : host; if (!m) return;
    switch (b.dataset.act) {
      case 'copy': await navigator.clipboard.writeText(m.content); b.innerHTML = ic('check'); setTimeout(() => b.innerHTML = ic('copy'), 1500); break;
      case 'regen': regenerate(host.id); break;
      case 'pick': pickCol(S.chat, host, m); break;
      case 'tool-allow': resolveTool(host.id, b.dataset.cid, 'allow'); break;
      case 'tool-always': resolveTool(host.id, b.dataset.cid, 'always'); break;
      case 'tool-deny': resolveTool(host.id, b.dataset.cid, 'deny'); break;
      case 'speak': if (S.speak.id === m.id) stopSpeak(); else speak(m.id, m.content); break;
      case 'branch': branchChat(S.chat, host); break;
      case 'fork-prev': forkStep(S.chat, host, -1); break;
      case 'fork-next': forkStep(S.chat, host, 1); break;
      case 'edit': editUser(el, m); break;
      case 'think': m._thinkOpen = !m._thinkOpen; b.closest('.think').classList.toggle('open', m._thinkOpen); break;
      case 'docs': m._docsOpen = !m._docsOpen; b.closest('.sources').classList.toggle('open', m._docsOpen); break;
      case 'sources': m._srcOpen = !m._srcOpen; b.closest('.sources').classList.toggle('open', m._srcOpen); break;
    }
  };

  scroller().addEventListener('scroll', updateJump, { passive: true });
  $('#jumpBtn').onclick = () => { scroller().scrollTo({ top: scroller().scrollHeight, behavior: 'smooth' }); };

  let dragN = 0;
  const main = $('#main');
  main.addEventListener('dragenter', e => { if ([...e.dataTransfer.types].includes('Files')) { dragN++; $('#dropZone').classList.remove('hidden'); } });
  main.addEventListener('dragleave', () => { if (--dragN <= 0) { dragN = 0; $('#dropZone').classList.add('hidden'); } });
  main.addEventListener('dragover', e => e.preventDefault());
  main.addEventListener('drop', e => { e.preventDefault(); dragN = 0; $('#dropZone').classList.add('hidden'); dropAttachments(e.dataTransfer); });

  // modals
  $$('[data-close]').forEach(b => b.onclick = () => b.closest('.modal').classList.add('hidden'));
  $$('.modal').forEach(m => m.addEventListener('mousedown', e => { if (e.target === m && m.id !== 'promptModal') m.classList.add('hidden'); }));
  $('#modelsBtn').onclick = openModels;
  $('#providersBtn').onclick = openProviders;
  $('#prefsBtn').onclick = () => $('#prefsModal').classList.remove('hidden');
  $('#pullBtn').onclick = pullModel;
  $('#pullCancel').onclick = () => pullCtl?.abort();
  $('#pullChips').onclick = e => { const n = e.target.closest('[data-pull]')?.dataset.pull; if (n) { $('#pullName').value = n; $('#pullName').focus(); } };
  $('#modelFilter').oninput = renderModelTable;
  $('#modelSort').onchange = renderModelTable;
  $('#modelsFix').onclick = async () => { await ollamaFix(); refreshModelsModal(); };
  $('#ollamaStatus').onclick = () => { if (!S.online) ollamaFix(); else openModels(); };
  $('#psList').onclick = async e => {
    const name = e.target.closest('[data-unload]')?.dataset.unload; if (!name) return;
    try { await api('/unload', { method: 'POST', body: JSON.stringify({ model: name }) }); toast('Unloaded ' + name); refreshModelsModal(); }
    catch (err) { toast(err.message, true); }
  };
  $('#pullName').onkeydown = e => { if (e.key === 'Enter') pullModel(); };
  $('#modelTable').onclick = async e => {
    const name = e.target.closest('[data-delmodel]')?.dataset.delmodel; if (!name) return;
    if (!await ask({ title: 'Delete model?', msg: `${name} will be removed from disk.`, input: false, okText: 'Delete', danger: true })) return;
    try { await api('/delete-model', { method: 'POST', body: JSON.stringify({ model: name }) }); toast('Deleted ' + name); delete S.caps[capKey('ollama', name)]; refreshModelsModal(); loadCatalog(); }
    catch (err) { toast(err.message, true); }
  };

  // providers
  $('#provList').addEventListener('click', e => {
    const b = e.target.closest('[data-act]'); if (!b || b.type === 'checkbox') return;
    providerAction(b.closest('.prov-card'), b.dataset.act, b);
  });
  $('#provList').addEventListener('change', e => {
    const b = e.target.closest('input[type=checkbox][data-act]');
    if (b) providerAction(b.closest('.prov-card'), b.dataset.act, b);
  });
  $('#provList').addEventListener('keydown', e => {
    const f = e.target.dataset?.f; if (e.key !== 'Enter' || !f) return;
    const card = e.target.closest('.prov-card');
    if (f === 'key') providerAction(card, 'savekey');
    else e.target.blur();
  });
  $('#provList').addEventListener('focusout', e => {
    const f = e.target.dataset?.f; if (f !== 'base' && f !== 'name') return;
    const card = e.target.closest('.prov-card'), p = provById(card.dataset.id);
    const v = e.target.value.trim(), cur = f === 'base' ? (p.base_url || '') : p.name;
    if (v !== cur) saveProviderField(card, f, v);
  });
  $('#provPresets').onclick = e => {
    const b = e.target.closest('button'); if (!b) return;
    $('#newProvName').value = b.dataset.n; $('#newProvUrl').value = b.dataset.u; $('#newProvLocal').checked = b.dataset.l === '1';
    $('#newProvKey').focus();
  };
  $('#newProvAdd').onclick = addProvider;
  $('#cloudHelpers').onchange = e => setSetting('cloudHelpers', e.target.checked);

  $('#themeSel').onclick = e => { const v = e.target.closest('button')?.dataset.v; if (v) { setSetting('theme', v); applyTheme(); } };
  $('#fontSel').onclick = e => { const v = e.target.closest('button')?.dataset.v; if (v) { setSetting('font', v); applyTheme(); } };
  $('#backupBtn').onclick = () => download('/backup', 'Guanaco-backup.zip');
  let restoreFile = null, restoreMode = 'merge';
  const modeNotes = { merge: 'Adds missing chats and updates older copies. Nothing is deleted.', replace: 'Deletes every current chat that is not in the backup, then restores the backup.' };
  const setMode = m => { restoreMode = m; $$('#restoreMode button').forEach(b => b.classList.toggle('on', b.dataset.v === m)); $('#restoreModeNote').textContent = modeNotes[m]; };
  $('#restoreBtn').onclick = () => $('#restoreFile').click();
  $('#restoreFile').onchange = e => {
    restoreFile = e.target.files[0]; e.target.value = ''; if (!restoreFile) return;
    $('#restoreFileName').textContent = `${restoreFile.name} (${fmtBytes(restoreFile.size)})`;
    $('#restoreSettings').checked = false; $('#restoreProviders').checked = false; $('#restoreProviders').disabled = !!S.lan; setMode('merge');
    $('#restoreModal').classList.remove('hidden');
  };
  $('#restoreMode').onclick = e => { const v = e.target.closest('button')?.dataset.v; if (v) setMode(v); };
  $('#restoreGo').onclick = async () => {
    if (!restoreFile) return;
    if (restoreMode === 'replace' && !await ask({ title: 'Replace all chats?', msg: 'Chats that are not in the backup will be deleted permanently.', input: false, okText: 'Replace', danger: true })) return;
    const qs = new URLSearchParams({ mode: restoreMode });
    const withSettings = $('#restoreSettings').checked, withProviders = $('#restoreProviders').checked;
    if (withSettings) qs.set('settings', '1'); if (withProviders) qs.set('providers', '1');
    try {
      $('#restoreGo').disabled = true;
      const r = await api('/restore?' + qs, { method: 'POST', headers: { 'Content-Type': 'application/zip' }, body: restoreFile });
      $('#restoreModal').classList.add('hidden');
      toast(`Restored ${r.imported} chats${r.skipped ? `, ${r.skipped} already up to date` : ''}${r.removed ? `, removed ${r.removed}` : ''}`);
      S.cache.clear(); await loadChats();
      if (r.settings || r.providers) setTimeout(() => location.reload(), 900);
    } catch (err) { toast('Restore failed: ' + err.message, true); }
    finally { $('#restoreGo').disabled = false; }
  };
  $('#exportAll').onclick = () => download('/export', 'Guanaco-export.json');
  $('#importBtn').onclick = () => $('#importFile').click();
  $('#importFile').onchange = async e => {
    const f = e.target.files[0]; e.target.value = ''; if (!f) return;
    try { const r = await api('/import', { method: 'POST', body: await f.text() }); toast(`Imported ${r.imported} chats${r.skipped ? `, ${r.skipped} already up to date` : ''}`); S.cache.clear(); loadChats(); }
    catch (err) { toast('Import failed: ' + err.message, true); }
  };
  $('#deleteAll').onclick = async () => {
    if (!await ask({ title: 'Delete all chats?', msg: `All ${S.chats.length} chats will be permanently deleted. Export first if you want a backup.`, input: false, okText: 'Delete all', danger: true })) return;
    for (const c of [...S.chats]) { S.streams.get(c.id)?.abort(); await api('/chats/' + c.id, { method: 'DELETE' }).catch(() => { }); }
    S.chats = []; S.cache.clear(); newChat(); $('#prefsModal').classList.add('hidden'); toast('All chats deleted');
  };

  document.addEventListener('keydown', e => {
    const mod = e.ctrlKey || e.metaKey;
    if (e.key === 'Escape') {
      const open = $$('.modal:not(.hidden)').filter(m => m.id !== 'promptModal');
      if (open.length) { open.forEach(m => m.classList.add('hidden')); return; }
      if (document.querySelector('.lightbox')) { document.querySelector('.lightbox').remove(); return; }
      if (S.chat && S.streams.has(S.chat.id)) { stop(); return; }
      closeFloating(); $('#modelMenu').classList.add('hidden'); $('#exportMenu').classList.add('hidden');
    }
    if (mod && e.shiftKey && e.key.toLowerCase() === 'o') { e.preventDefault(); newChat(); }
    if (mod && !e.shiftKey && e.key.toLowerCase() === 'k') { e.preventDefault(); setSidebar(true); $$('.seg-btn')[0].click(); $('#chatSearch').focus(); }
    if (mod && !e.shiftKey && e.key === '/') { e.preventDefault(); openModelMenu(); }
    if (mod && e.key === '.') { e.preventDefault(); setSidebar($('#app').classList.contains('collapsed')); }
  });

  window.addEventListener('hashchange', () => { const id = idFromHash(); if (id && id !== S.chat?.id) openChat(id); });
}

function lightbox(src) {
  const d = document.createElement('div');
  d.className = 'lightbox modal';
  d.innerHTML = `<img src="${src}" style="max-width:92vw;max-height:90vh;border-radius:12px;box-shadow:var(--shadow-lg)">`;
  d.onclick = () => d.remove();
  document.body.appendChild(d);
}

export const idFromHash = () => (location.hash.match(/^#\/c\/([A-Za-z0-9_-]+)/) || [])[1];
