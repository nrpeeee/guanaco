// Chat list, loading and saving
import { exportChat } from './export-import.js';
import { newChat } from './nav.js';
import { isLocal, provName } from './providers.js';
import { renderTopbar } from './render.js';
import { S } from './state.js';
import { $, $$, api, ask, esc, ic, toast } from './util.js';

// ───────────────────────── chats list ─────────────────────────
export async function loadChats() {
  try { S.chats = await api('/chats'); } catch { S.chats = []; }
  renderChatList();
}

export function upsertMeta(chat) {
  const meta = { id: chat.id, title: chat.title, model: chat.model, provider: chat.provider, created: chat.created, updated: chat.updated, pinned: !!chat.pinned };
  const i = S.chats.findIndex(c => c.id === chat.id);
  if (i >= 0) S.chats[i] = meta; else S.chats.unshift(meta);
  S.chats.sort((a, b) => b.updated - a.updated);
  renderChatList();
}

export function renderChatList() {
  const q = $('#chatSearch').value.trim().toLowerCase();
  const list = S.chats.filter(c => !q || (c.title || '').toLowerCase().includes(q));
  const el = $('#chatList');
  if (!list.length) { el.innerHTML = `<div class="empty-list">${q ? 'No matching chats' : 'No chats yet'}</div>`; return; }
  const now = new Date(), day = 864e5;
  const startToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const groupOf = c => c.pinned ? 'Starred' : c.updated >= startToday ? 'Today' : c.updated >= startToday - day ? 'Yesterday'
    : c.updated >= startToday - 7 * day ? 'Previous 7 days' : c.updated >= startToday - 30 * day ? 'Previous 30 days' : 'Older';
  const order = ['Starred', 'Today', 'Yesterday', 'Previous 7 days', 'Previous 30 days', 'Older'];
  const groups = {};
  for (const c of list) (groups[groupOf(c)] ??= []).push(c);
  el.innerHTML = order.filter(g => groups[g]).map(g => `<div class="group">${g}</div>` + groups[g].map(c =>
    `<div class="chat-item${S.chat?.id === c.id ? ' active' : ''}${S.streams.has(c.id) ? ' streaming' : ''}" data-id="${c.id}">
      ${c.pinned ? `<span class="star">${ic('starf')}</span>` : ''}<span class="t">${esc(c.title || 'New chat')}</span>
      ${c.provider && !isLocal(c.provider) ? `<span class="ci-cloud" title="${esc(provName(c.provider))}">${ic('cloud')}</span>` : ''}
      <button class="more" data-more="${c.id}" title="Options">${ic('more')}</button></div>`).join('')).join('');
}

let floatingMenu = null;
export function closeFloating() { floatingMenu?.remove(); floatingMenu = null; $$('.more.open').forEach(b => b.classList.remove('open')); }
export function chatMenu(btn, id) {
  closeFloating();
  const meta = S.chats.find(c => c.id === id); if (!meta) return;
  const m = document.createElement('div');
  m.className = 'menu floating';
  m.innerHTML = `<button data-a="pin">${ic(meta.pinned ? 'star' : 'starf')}${meta.pinned ? 'Unstar' : 'Star'}</button>
    <button data-a="rename">${ic('edit')}Rename</button>
    <button data-a="md">${ic('download')}Export Markdown</button>
    <button data-a="json">${ic('code')}Export JSON</button><hr>
    <button data-a="del" class="danger">${ic('trash')}Delete</button>`;
  document.body.appendChild(m);
  const r = btn.getBoundingClientRect();
  m.style.left = Math.min(r.left, innerWidth - 220) + 'px';
  m.style.top = Math.min(r.bottom + 4, innerHeight - m.offsetHeight - 8) + 'px';
  btn.classList.add('open'); floatingMenu = m;
  m.onclick = async e => {
    const a = e.target.closest('button')?.dataset.a; if (!a) return;
    closeFloating();
    if (a === 'pin') { const c = await fullChat(id); c.pinned = !c.pinned; await saveChat(c, false); }
    if (a === 'rename') renameChat(id);
    if (a === 'md' || a === 'json') exportChat(id, a);
    if (a === 'del') deleteChat(id);
  };
}

export async function fullChat(id) {
  if (S.cache.has(id)) return S.cache.get(id);
  const c = await api('/chats/' + id);
  c.messages ??= [];
  c.provider ??= 'ollama';
  S.cache.set(id, c);
  return c;
}

export async function renameChat(id) {
  const c = await fullChat(id);
  const t = await ask({ title: 'Rename chat', value: c.title || '' });
  if (t == null || !t.trim()) return;
  c.title = t.trim(); await saveChat(c, false);
  if (S.chat?.id === id) renderTopbar();
}

async function deleteChat(id) {
  const meta = S.chats.find(c => c.id === id);
  const ok = await ask({ title: 'Delete chat?', msg: `“${meta?.title || 'New chat'}” will be permanently deleted.`, input: false, okText: 'Delete', danger: true });
  if (!ok) return;
  S.streams.get(id)?.abort();
  await api('/chats/' + id, { method: 'DELETE' }).catch(e => toast(e.message, true));
  S.cache.delete(id); S.chats = S.chats.filter(c => c.id !== id);
  if (S.chat?.id === id) newChat(); else renderChatList();
}

export function cleanForSave(chat) {
  return JSON.stringify(chat, (k, v) => (k.startsWith('_') || k === 'status') ? undefined : v);
}
export async function saveChat(chat, touch = true) {
  if (touch) chat.updated = Date.now();
  try { await api('/chats/' + chat.id, { method: 'PUT', body: cleanForSave(chat) }); }
  catch (e) { toast('Save failed: ' + e.message, true); }
  upsertMeta(chat);
}
