// Prompt library and slash menu
import { autosize, updateSendBtn } from './composer.js';
import { S, saveSettings } from './state.js';
import { $, esc, ic, toast, uid } from './util.js';

// ───────────────────────── prompt library ─────────────────────────
export const PH_RE = /\{\{[^}\n]*\}\}/g;
function slashItems(q) {
  q = (q || '').toLowerCase();
  return S.settings.prompts.filter(p => !q || p.name.toLowerCase().includes(q) || p.text.toLowerCase().includes(q));
}
function renderSlash() {
  const m = $('#slashMenu'), its = S.slash.items;
  m.innerHTML = (its.length ? its.map((p, i) => `<button type="button" class="sl-item${i === S.slash.idx ? ' sel' : ''}" data-i="${i}"><b>${esc(p.name)}</b><span>${esc(p.text.replace(/\s+/g, ' ').slice(0, 100))}</span></button>`).join('')
    : '<div class="mm-empty">No matching prompts</div>')
    + `<div class="sl-foot"><button type="button" data-sl="save">${ic('plus')}Save message as prompt</button><button type="button" data-sl="manage">${ic('book')}Manage prompts…</button></div>`;
  $('.sl-item.sel', m)?.scrollIntoView({ block: 'nearest' });
}
export function openSlash(q) { S.slash = { open: true, items: slashItems(q), idx: 0 }; $('#slashMenu').classList.remove('hidden'); renderSlash(); }
export function closeSlash() { S.slash.open = false; $('#slashMenu').classList.add('hidden'); }
export function nextPlaceholder(from) {
  const ta = $('#input'); PH_RE.lastIndex = from;
  const m = PH_RE.exec(ta.value); if (!m) return false;
  ta.setSelectionRange(m.index, m.index + m[0].length); return true;
}
export function usePrompt(p) {
  const ta = $('#input');
  ta.value = p.text; autosize(); updateSendBtn(); closeSlash(); ta.focus();
  if (!nextPlaceholder(0)) ta.setSelectionRange(ta.value.length, ta.value.length);
}
export function slashKey(e) {
  if (!S.slash.open) return false;
  const n = S.slash.items.length;
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); if (n) { S.slash.idx = (S.slash.idx + (e.key === 'ArrowDown' ? 1 : n - 1)) % n; renderSlash(); } return true; }
  if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); if (n) usePrompt(S.slash.items[S.slash.idx]); return true; }
  if (e.key === 'Escape') { e.preventDefault(); closeSlash(); return true; }
  return false;
}
export function renderPromptList() {
  const L = S.settings.prompts;
  $('#promptList').innerHTML = L.length ? L.map(p => `<div class="pl-row" data-id="${esc(p.id)}"><div class="pl-txt"><b>${esc(p.name)}</b><span>${esc(p.text.replace(/\s+/g, ' ').slice(0, 110))}</span></div>
    <button class="icon-btn sm" data-pl="edit" title="Edit">${ic('edit')}</button><button class="icon-btn sm" data-pl="del" title="Delete">${ic('trash')}</button></div>`).join('')
    : '<div class="muted small">No prompts yet.</div>';
}
export function openPrompts(seedText) {
  closeSlash(); S.plEdit = null;
  $('#plName').value = ''; $('#plText').value = seedText || ''; $('#plSave').textContent = 'Add prompt';
  renderPromptList(); $('#promptsModal').classList.remove('hidden');
  setTimeout(() => (seedText ? $('#plName') : $('#plName')).focus(), 20);
}
export function savePromptForm() {
  const name = $('#plName').value.trim(), text = $('#plText').value;
  if (!name || !text.trim()) { toast('A prompt needs a name and some text', true); return; }
  const ex = S.plEdit && S.settings.prompts.find(p => p.id === S.plEdit);
  if (ex) { ex.name = name; ex.text = text; } else S.settings.prompts.push({ id: uid(), name, text });
  saveSettings(); S.plEdit = null;
  $('#plName').value = ''; $('#plText').value = ''; $('#plSave').textContent = 'Add prompt';
  renderPromptList();
}
